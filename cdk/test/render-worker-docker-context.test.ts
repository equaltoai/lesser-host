import assert from "node:assert/strict";
import {
  cpSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve, sep } from "node:path";
import test from "node:test";

import * as cdk from "aws-cdk-lib";
import * as ecrassets from "aws-cdk-lib/aws-ecr-assets";

import { LesserHostStack } from "../lib/lesser-host-stack";
import {
  webStackEnv,
  writeTestAppTheoryConfig,
} from "./_lesser-host-test-helpers";

const renderWorkerDockerfile = "cmd/render-worker/Dockerfile";
const dockerAssetExcludes = [
  "cdk/cdk.out/**",
  "cdk/node_modules/**",
  "cdk/.build/**",
  ".git/**",
  "**/.env",
];

type DockerImageManifestEntry = {
  source?: {
    directory?: unknown;
    dockerFile?: unknown;
  };
  destinations?: Record<string, { imageTag?: unknown }>;
};

type DockerAsset = {
  hash: string;
  sourceDirectory: string;
  files: string[];
};

type AssetStagingInspection = {
  fingerprintOptions?: { ignoreMode?: unknown };
};

type DockerImageAssetInspection = {
  dockerfilePath?: unknown;
};

function repoRoot(): string {
  return resolve(process.cwd(), "..");
}

function walkRegularFiles(root: string): string[] {
  const files: string[] = [];

  function walk(current: string): void {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const absolute = join(current, entry.name);
      assert.equal(
        entry.isSymbolicLink(),
        false,
        `Docker context must not contain symlinks: ${relative(root, absolute)}`,
      );
      if (entry.isDirectory()) {
        walk(absolute);
        continue;
      }
      assert.equal(
        entry.isFile(),
        true,
        `Docker context must contain regular files only: ${relative(root, absolute)}`,
      );
      files.push(relative(root, absolute).split(sep).join("/"));
    }
  }

  walk(root);
  return files.sort();
}

function parseRenderWorkerDockerAsset(
  assemblyDirectory: string,
  stackArtifactId: string,
): DockerAsset {
  const manifestPath = join(
    assemblyDirectory,
    `${stackArtifactId}.assets.json`,
  );
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as {
    dockerImages?: Record<string, DockerImageManifestEntry>;
  };
  const renderWorkerEntries = Object.entries(manifest.dockerImages ?? {}).filter(
    ([, entry]) => entry.source?.dockerFile === renderWorkerDockerfile,
  );
  assert.equal(
    renderWorkerEntries.length,
    1,
    "expected exactly one RenderWorker Docker image in the synthesized stack asset manifest",
  );

  const [hash, entry] = renderWorkerEntries[0]!;
  assert.match(hash, /^[a-f0-9]{64}$/);
  const sourceDirectoryValue = entry.source?.directory;
  assert.equal(
    typeof sourceDirectoryValue,
    "string",
    "expected RenderWorker asset source directory",
  );
  const sourceDirectory = realpathSync(
    resolve(assemblyDirectory, sourceDirectoryValue as string),
  );
  const realAssemblyDirectory = realpathSync(assemblyDirectory);
  assert.ok(
    sourceDirectory.startsWith(`${realAssemblyDirectory}${sep}`),
    "RenderWorker asset source must be staged inside the CDK assembly",
  );
  assert.equal(
    sourceDirectory.split(sep).at(-1),
    `asset.${hash}`,
    "RenderWorker asset directory must be content-addressed by the manifest hash",
  );

  for (const destination of Object.values(entry.destinations ?? {})) {
    assert.equal(
      destination.imageTag,
      hash,
      "RenderWorker image destination must use the content hash as its tag",
    );
  }

  return {
    hash,
    sourceDirectory,
    files: walkRegularFiles(sourceDirectory),
  };
}

function synthesizeStackRenderWorkerAsset(outdir: string): DockerAsset {
  const appConfigPath = join(outdir, "app.json");
  writeTestAppTheoryConfig(appConfigPath);
  const app = new cdk.App({ outdir });
  const stack = new LesserHostStack(app, "RenderWorkerDockerContextTest", {
    stage: "lab",
    env: webStackEnv,
    appConfigPath,
  });
  const renderAssets = stack.node.findAll().filter(
    (child): child is ecrassets.DockerImageAsset =>
      child instanceof ecrassets.DockerImageAsset &&
      (child as unknown as DockerImageAssetInspection).dockerfilePath ===
        renderWorkerDockerfile,
  );
  assert.equal(
    renderAssets.length,
    1,
    "expected exactly one RenderWorker DockerImageAsset construct",
  );
  const staging = renderAssets[0]!.node.tryFindChild("Staging");
  assert.ok(
    staging instanceof cdk.AssetStaging,
    "expected RenderWorker AssetStaging child",
  );
  assert.equal(
    (staging as unknown as AssetStagingInspection).fingerprintOptions
      ?.ignoreMode,
    cdk.IgnoreMode.DOCKER,
    "RenderWorker asset must pin Docker ignore semantics",
  );
  const assembly = app.synth();
  return parseRenderWorkerDockerAsset(assembly.directory, stack.artifactId);
}

function synthesizeFixtureDockerAsset(
  fixtureDirectory: string,
  outdir: string,
  stackId: string,
): DockerAsset {
  const app = new cdk.App({ outdir });
  const stack = new cdk.Stack(app, stackId, { env: webStackEnv });
  const asset = new ecrassets.DockerImageAsset(stack, "RenderWorker", {
    directory: fixtureDirectory,
    file: renderWorkerDockerfile,
    ignoreMode: cdk.IgnoreMode.DOCKER,
    exclude: dockerAssetExcludes,
  });
  const assembly = app.synth();
  const synthesized = parseRenderWorkerDockerAsset(
    assembly.directory,
    stack.artifactId,
  );
  assert.equal(
    synthesized.hash,
    asset.assetHash,
    "asset manifest hash must match the DockerImageAsset hash",
  );
  return synthesized;
}

function expectedRenderWorkerFiles(root: string): string[] {
  const expected = [
    ".dockerignore",
    renderWorkerDockerfile,
    "go.mod",
    "go.sum",
  ];

  for (const sourceRoot of ["cmd/render-worker", "internal"]) {
    const absoluteSourceRoot = join(root, sourceRoot);
    for (const file of walkRegularFiles(absoluteSourceRoot)) {
      if (file.endsWith(".go") && !file.endsWith("_test.go")) {
        expected.push(`${sourceRoot}/${file}`);
      }
    }
  }
  return expected.sort();
}

function isRenderWorkerAllowlistedFile(file: string): boolean {
  if (
    file === ".dockerignore" ||
    file === renderWorkerDockerfile ||
    file === "go.mod" ||
    file === "go.sum"
  ) {
    return true;
  }
  if (file.endsWith("_test.go")) {
    return false;
  }
  return (
    /^cmd\/render-worker\/[^/]+\.go$/.test(file) ||
    /^internal\/(?:[^/]+\/)*[^/]+\.go$/.test(file)
  );
}

function writeSentinel(root: string, file: string): void {
  const absolute = join(root, file);
  mkdirSync(dirname(absolute), { recursive: true });
  writeFileSync(absolute, "render-worker-context-forbidden-sentinel\n");
}

test(
  "RenderWorker Docker asset is default-deny, secret-safe, and content-addressed",
  { timeout: 180_000 },
  () => {
    const actualAssembly = mkdtempSync(
      join(tmpdir(), "lesser-host-render-stack-"),
    );
    const fixtures = mkdtempSync(
      join(tmpdir(), "lesser-host-render-fixtures-"),
    );

    try {
      const root = repoRoot();
      const stackSource = readFileSync(
        join(process.cwd(), "lib", "lesser-host-stack.ts"),
        "utf8",
      );
      const renderAssetCall = stackSource.match(
        /DockerImageCode\.fromImageAsset\(repoRoot,\s*\{([\s\S]*?)\n\s*\}\),/,
      );
      assert.ok(
        renderAssetCall,
        "expected the RenderWorker repo-root Docker image asset source",
      );
      assert.match(
        renderAssetCall[1]!,
        /^\s*ignoreMode:\s*cdk\.IgnoreMode\.DOCKER,?\s*$/m,
        "repo-root Docker asset must explicitly pin Docker ignore semantics",
      );

      const dockerIgnoreRules = readFileSync(
        join(root, ".dockerignore"),
        "utf8",
      )
        .split(/\r?\n/)
        .map((line) => line.trim())
        .filter((line) => line.length > 0 && !line.startsWith("#"));
      assert.deepEqual(dockerIgnoreRules, [
        "**",
        "!go.mod",
        "!go.sum",
        "!cmd/render-worker/*.go",
        "!cmd/render-worker/Dockerfile",
        "!internal/**/*.go",
        "**/*_test.go",
      ]);

      const dockerfile = readFileSync(
        join(root, renderWorkerDockerfile),
        "utf8",
      );
      assert.match(dockerfile, /^COPY cmd\/render-worker \.\/cmd\/render-worker$/m);
      assert.match(dockerfile, /^COPY internal \.\/internal$/m);
      assert.doesNotMatch(
        dockerfile,
        /^COPY\s+\.\s+\.$/m,
        "RenderWorker Dockerfile must not copy the repository wholesale",
      );

      const actual = synthesizeStackRenderWorkerAsset(actualAssembly);
      assert.deepEqual(
        actual.files,
        expectedRenderWorkerFiles(root),
        "synthesized RenderWorker asset must contain exactly the required non-test Go build inputs",
      );
      assert.ok(actual.files.every(isRenderWorkerAllowlistedFile));
      for (const required of [
        ".dockerignore",
        "go.mod",
        "go.sum",
        renderWorkerDockerfile,
        "cmd/render-worker/main.go",
        "internal/renderworker/app.go",
      ]) {
        assert.ok(
          actual.files.includes(required),
          `expected required RenderWorker build input ${required}`,
        );
      }
      for (const forbidden of [
        ".aws/",
        ".git/",
        ".theory/",
        ".codex/",
        ".agents/",
        "cdk/",
        "contracts/",
        "docs/",
        "scripts/",
        "web/",
      ]) {
        assert.equal(
          actual.files.some(
            (file) => file === forbidden.slice(0, -1) || file.startsWith(forbidden),
          ),
          false,
          `forbidden operator/generated path entered Docker context: ${forbidden}`,
        );
      }
      assert.equal(
        actual.files.some(
          (file) => file === ".env" || file.endsWith("/.env"),
        ),
        false,
      );
      assert.equal(
        actual.files.some((file) => file.endsWith("_test.go")),
        false,
      );

      const ignoredFixture = join(fixtures, "ignored");
      cpSync(actual.sourceDirectory, ignoredFixture, { recursive: true });
      const ignoredSentinels = [
        ".aws/credentials",
        ".theory/operator.json",
        ".codex/session.json",
        ".agents/local.json",
        ".env",
        "cdk/cdk.context.local.json",
        "cdk/cdk.out/template.json",
        "cdk/.build/bootstrap",
        "contracts/artifacts/contract.json",
        "scripts/.lab-e2e-instance-key",
        "web/dist/index.html",
        "internal/renderworker/context_sentinel_test.go",
        "operator-notes.txt",
      ];
      for (const sentinel of ignoredSentinels) {
        writeSentinel(ignoredFixture, sentinel);
      }
      const ignored = synthesizeFixtureDockerAsset(
        ignoredFixture,
        join(fixtures, "ignored-assembly"),
        "RenderWorkerIgnoredFixture",
      );
      assert.equal(
        ignored.hash,
        actual.hash,
        "operator-local, generated, test, and arbitrary ignored files must not alter the image asset hash",
      );
      assert.deepEqual(
        ignored.files,
        actual.files,
        "ignored sentinels must not enter the staged Docker context",
      );
      for (const sentinel of ignoredSentinels) {
        assert.equal(
          ignored.files.includes(sentinel),
          false,
          `ignored sentinel entered staged context: ${sentinel}`,
        );
      }

      const sourceFixture = join(fixtures, "source-change");
      cpSync(actual.sourceDirectory, sourceFixture, { recursive: true });
      const changedSource = join(
        sourceFixture,
        "internal",
        "renderworker",
        "doc.go",
      );
      assert.ok(statSync(changedSource).isFile());
      writeFileSync(
        changedSource,
        `${readFileSync(changedSource, "utf8")}\n// Docker asset hash sensitivity sentinel.\n`,
      );
      const changed = synthesizeFixtureDockerAsset(
        sourceFixture,
        join(fixtures, "source-change-assembly"),
        "RenderWorkerSourceFixture",
      );
      assert.notEqual(
        changed.hash,
        actual.hash,
        "a permitted RenderWorker Go source change must alter the image asset hash",
      );
      assert.ok(changed.files.includes("internal/renderworker/doc.go"));
    } finally {
      rmSync(actualAssembly, { recursive: true, force: true });
      rmSync(fixtures, { recursive: true, force: true });
    }
  },
);
