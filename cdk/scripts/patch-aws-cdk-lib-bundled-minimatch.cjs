const fs = require('node:fs');
const path = require('node:path');

function readJSON(filePath) {
	return JSON.parse(fs.readFileSync(filePath, 'utf8'));
}

function parseSemver(version) {
	const match = /^(\d+)\.(\d+)\.(\d+)(?:-.+)?$/.exec(version);
	if (!match) return null;
	return { major: Number(match[1]), minor: Number(match[2]), patch: Number(match[3]) };
}

function gte(a, b) {
	if (a.major !== b.major) return a.major > b.major;
	if (a.minor !== b.minor) return a.minor > b.minor;
	return a.patch >= b.patch;
}

function copyDir(sourceDir, destDir) {
	fs.mkdirSync(path.dirname(destDir), { recursive: true });
	fs.rmSync(destDir, { recursive: true, force: true });
	fs.cpSync(sourceDir, destDir, { recursive: true });
}

function main() {
	const projectRoot = path.resolve(__dirname, '..');
	const bundledMinimatchDir = path.join(
		projectRoot,
		'node_modules',
		'aws-cdk-lib',
		'node_modules',
		'minimatch',
	);
	const bundledMinimatchPkg = path.join(bundledMinimatchDir, 'package.json');
	const patchedMinimatchDir = path.join(projectRoot, 'node_modules', 'minimatch');
	const patchedMinimatchPkg = path.join(patchedMinimatchDir, 'package.json');

	if (!fs.existsSync(bundledMinimatchPkg) || !fs.existsSync(patchedMinimatchPkg)) return;

	const bundledVersion = readJSON(bundledMinimatchPkg).version;
	const patchedVersion = readJSON(patchedMinimatchPkg).version;

	if (bundledVersion === patchedVersion) return;

	const bundledSemver = parseSemver(bundledVersion);
	const patchedSemver = parseSemver(patchedVersion);
	if (!bundledSemver || !patchedSemver) return;

	// Only patch aws-cdk-lib's bundled minimatch 10.x (to avoid breaking older CDK releases).
	if (bundledSemver.major !== 10 || patchedSemver.major !== 10) return;

	// GHSA-7r86-cg39-jmmj / GHSA-23c5-xmqv-rm74 affect minimatch <=10.2.2.
	const fixedFloor = { major: 10, minor: 2, patch: 3 };
	if (gte(bundledSemver, fixedFloor)) return;

	copyDir(patchedMinimatchDir, bundledMinimatchDir);
	console.log(
		`Patched aws-cdk-lib bundled minimatch ${bundledVersion} -> ${patchedVersion} to address npm audit.`,
	);
}

main();

