import * as path from "node:path";
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as cdk from "aws-cdk-lib";
import {
  AppTheorySsrSite,
  AppTheorySsrSiteMode,
} from "@theory-cloud/apptheory-cdk";
import * as acm from "aws-cdk-lib/aws-certificatemanager";
import * as cloudfront from "aws-cdk-lib/aws-cloudfront";
import * as origins from "aws-cdk-lib/aws-cloudfront-origins";
import * as apigw from "aws-cdk-lib/aws-apigateway";
import * as apigwv2 from "aws-cdk-lib/aws-apigatewayv2";
import * as lambda from "aws-cdk-lib/aws-lambda";
import * as logs from "aws-cdk-lib/aws-logs";
import * as route53 from "aws-cdk-lib/aws-route53";
import * as route53Targets from "aws-cdk-lib/aws-route53-targets";
import * as s3 from "aws-cdk-lib/aws-s3";
import * as s3deploy from "aws-cdk-lib/aws-s3-deployment";
import * as wafv2 from "aws-cdk-lib/aws-wafv2";
import { Construct } from "constructs";
import { readWebDomainConfig } from "./app-theory-deploy-config";
import { hostWebAclRules } from "./web-acl-rules";

export interface WebDeliveryConfig {
  stage: string;
  namePrefix: string;
  appConfigPath?: string;
  repoRoot: string;
  removalPolicy: cdk.RemovalPolicy;
  controlPlaneApi: apigwv2.HttpApi;
  controlPlaneSseApi: apigw.LambdaRestApi;
  trustApi: apigwv2.HttpApi;
  controlPlaneFunction: lambda.Function;
  trustFunction: lambda.Function;
  soulEmailInboundDomain: string;
  ensureWebBuild: () => void;
}

export interface WebDeliveryResult {
  publicBaseUrl: string;
  webDistributionDomainName: string;
  webUrl: string;
}

export function shouldUseLocalWebBundling(
  env: NodeJS.ProcessEnv = process.env,
): boolean {
  return env.CI !== "true" && env.GITHUB_ACTIONS !== "true";
}

export function configureWebDelivery(
  scope: Construct,
  config: WebDeliveryConfig,
): WebDeliveryResult {
  const {
    stage,
    namePrefix,
    appConfigPath,
    repoRoot,
    removalPolicy,
    controlPlaneApi,
    controlPlaneSseApi,
    trustApi,
    controlPlaneFunction,
    trustFunction,
    soulEmailInboundDomain,
    ensureWebBuild,
  } = config;

  const webDomain = readWebDomainConfig(stage, appConfigPath);
  const webDomainName =
    stage === "live"
      ? webDomain.rootDomain
      : `${stage}.${webDomain.rootDomain}`;

  const webBucket = new s3.Bucket(scope, "WebBucket", {
    bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-web`,
    blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
    enforceSSL: true,
    removalPolicy,
    autoDeleteObjects: stage !== "live",
  });

  const accessLogsBucket = new s3.Bucket(scope, "AccessLogsBucket", {
    bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-access-logs`,
    accessControl: s3.BucketAccessControl.LOG_DELIVERY_WRITE,
    objectOwnership: s3.ObjectOwnership.OBJECT_WRITER,
    blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
    enforceSSL: true,
    lifecycleRules: [
      {
        id: "ExpireAccessLogs",
        expiration: cdk.Duration.days(stage === "live" ? 180 : 30),
      },
    ],
    removalPolicy,
    autoDeleteObjects: stage !== "live",
  });

  const webCsp = [
    "default-src 'none'",
    "base-uri 'none'",
    "object-src 'none'",
    "frame-ancestors 'none'",
    "form-action 'self'",
    "img-src 'self' data: blob:",
    "font-src 'self'",
    "style-src 'self'",
    "script-src 'self'",
    "connect-src 'self'",
    "manifest-src 'self'",
  ].join("; ");
  const safeAppCsp = [
    "default-src 'none'",
    "base-uri 'none'",
    "object-src 'none'",
    "frame-ancestors https://safe.global https://*.safe.global",
    "form-action 'self'",
    "img-src 'self' data: blob:",
    "font-src 'self'",
    "style-src 'self'",
    "script-src 'self'",
    "connect-src 'self'",
    "manifest-src 'self'",
  ].join("; ");

  const webSecurityHeaders = new cloudfront.ResponseHeadersPolicy(
    scope,
    "WebSecurityHeaders",
    {
      responseHeadersPolicyName: `${namePrefix}-web-security`,
      securityHeadersBehavior: {
        contentSecurityPolicy: {
          contentSecurityPolicy: webCsp,
          override: true,
        },
        contentTypeOptions: { override: true },
        frameOptions: {
          frameOption: cloudfront.HeadersFrameOption.DENY,
          override: true,
        },
        referrerPolicy: {
          referrerPolicy: cloudfront.HeadersReferrerPolicy.SAME_ORIGIN,
          override: true,
        },
        strictTransportSecurity: {
          accessControlMaxAge: cdk.Duration.days(365),
          includeSubdomains: true,
          preload: true,
          override: true,
        },
        xssProtection: { protection: true, modeBlock: true, override: true },
      },
      customHeadersBehavior: {
        customHeaders: [
          {
            header: "Permissions-Policy",
            value:
              "camera=(), microphone=(), geolocation=(), interest-cohort=()",
            override: true,
          },
        ],
      },
    },
  );
  const safeAppSecurityHeaders = new cloudfront.ResponseHeadersPolicy(
    scope,
    "SafeAppSecurityHeaders",
    {
      responseHeadersPolicyName: `${namePrefix}-safe-app-security`,
      corsBehavior: {
        accessControlAllowCredentials: false,
        accessControlAllowHeaders: [
          "Authorization",
          "Content-Type",
          "X-Requested-With",
        ],
        accessControlAllowMethods: ["GET", "HEAD", "OPTIONS"],
        accessControlAllowOrigins: ["*"],
        accessControlMaxAge: cdk.Duration.minutes(10),
        originOverride: true,
      },
      securityHeadersBehavior: {
        contentSecurityPolicy: {
          contentSecurityPolicy: safeAppCsp,
          override: true,
        },
        contentTypeOptions: { override: true },
        referrerPolicy: {
          referrerPolicy: cloudfront.HeadersReferrerPolicy.SAME_ORIGIN,
          override: true,
        },
        strictTransportSecurity: {
          accessControlMaxAge: cdk.Duration.days(365),
          includeSubdomains: true,
          preload: true,
          override: true,
        },
        xssProtection: { protection: true, modeBlock: true, override: true },
      },
      customHeadersBehavior: {
        customHeaders: [
          {
            header: "Permissions-Policy",
            value:
              "camera=(), microphone=(), geolocation=(), interest-cohort=()",
            override: true,
          },
        ],
      },
    },
  );

  const apiSecurityHeaders = new cloudfront.ResponseHeadersPolicy(
    scope,
    "ApiSecurityHeaders",
    {
      responseHeadersPolicyName: `${namePrefix}-api-security`,
      securityHeadersBehavior: {
        contentTypeOptions: { override: true },
        frameOptions: {
          frameOption: cloudfront.HeadersFrameOption.DENY,
          override: true,
        },
        referrerPolicy: {
          referrerPolicy: cloudfront.HeadersReferrerPolicy.SAME_ORIGIN,
          override: true,
        },
        strictTransportSecurity: {
          accessControlMaxAge: cdk.Duration.days(365),
          includeSubdomains: true,
          preload: true,
          override: true,
        },
        xssProtection: { protection: true, modeBlock: true, override: true },
      },
      customHeadersBehavior: {
        customHeaders: [
          {
            header: "Permissions-Policy",
            value:
              "camera=(), microphone=(), geolocation=(), interest-cohort=()",
            override: true,
          },
        ],
      },
    },
  );

  const webSpaRewriteFn = new cloudfront.Function(scope, "WebSpaRewriteFn", {
    functionName: `${namePrefix}-web-spa-rewrite`,
    code: cloudfront.FunctionCode.fromInline(`function handler(event) {
  var request = event.request;
  var uri = request.uri || "/";

  // Never rewrite API routes.
  var isSetupApi =
  uri === "/setup/status" ||
  uri === "/setup/admin" ||
  uri === "/setup/finalize" ||
  uri.startsWith("/setup/bootstrap/");

  if (
  uri.startsWith("/api/") ||
  uri.startsWith("/auth/") ||
  uri.startsWith("/webhooks/") ||
  uri.startsWith("/resolve") ||
  uri.startsWith("/health") ||
  isSetupApi ||
  uri.startsWith("/.well-known/") ||
  uri.startsWith("/attestations")
  ) {
  return request;
  }

  // If the path looks like a file, do not rewrite.
  if (uri.indexOf(".") !== -1) {
  return request;
  }

  request.uri = "/index.html";
  return request;
}`),
  });

  const webZone = route53.HostedZone.fromHostedZoneAttributes(
    scope,
    "WebHostedZone",
    {
      hostedZoneId: webDomain.hostedZoneId,
      zoneName: webDomain.hostedZoneName,
    },
  );
  const webCert = new acm.DnsValidatedCertificate(scope, "WebCertificate", {
    domainName: webDomainName,
    hostedZone: webZone,
    region: "us-east-1",
  });

  const webOai = new cloudfront.OriginAccessIdentity(scope, "WebOAI");
  webBucket.grantRead(webOai);

  const controlPlaneDomain = `${controlPlaneApi.httpApiId}.execute-api.${cdk.Aws.REGION}.amazonaws.com`;
  const trustDomain = `${trustApi.httpApiId}.execute-api.${cdk.Aws.REGION}.amazonaws.com`;

  const controlPlaneOrigin = new origins.HttpOrigin(controlPlaneDomain, {
    protocolPolicy: cloudfront.OriginProtocolPolicy.HTTPS_ONLY,
  });
  // Avoid RestApiOrigin: its generated DeploymentStage edge cycles through PUBLIC_BASE_URL.
  const controlPlaneSseOrigin = new origins.HttpOrigin(
    cdk.Fn.join("", [
      controlPlaneSseApi.restApiId,
      `.execute-api.${cdk.Aws.REGION}.`,
      cdk.Aws.URL_SUFFIX,
    ]),
    {
      originPath: `/${stage}`,
      protocolPolicy: cloudfront.OriginProtocolPolicy.HTTPS_ONLY,
      readTimeout: cdk.Duration.seconds(120),
      responseCompletionTimeout: cdk.Duration.seconds(180),
    },
  );
  const trustOrigin = new origins.HttpOrigin(trustDomain, {
    protocolPolicy: cloudfront.OriginProtocolPolicy.HTTPS_ONLY,
  });

  const apiBehavior: cloudfront.BehaviorOptions = {
    origin: controlPlaneOrigin,
    viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
    allowedMethods: cloudfront.AllowedMethods.ALLOW_ALL,
    cachePolicy: cloudfront.CachePolicy.CACHING_DISABLED,
    originRequestPolicy:
      cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
    responseHeadersPolicy: apiSecurityHeaders,
  };
  const apiSseBehavior: cloudfront.BehaviorOptions = {
    origin: controlPlaneSseOrigin,
    viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
    allowedMethods: cloudfront.AllowedMethods.ALLOW_ALL,
    cachePolicy: cloudfront.CachePolicy.CACHING_DISABLED,
    originRequestPolicy:
      cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
    responseHeadersPolicy: apiSecurityHeaders,
  };

  const trustApiBehavior: cloudfront.BehaviorOptions = {
    origin: trustOrigin,
    viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
    allowedMethods: cloudfront.AllowedMethods.ALLOW_ALL,
    cachePolicy: cloudfront.CachePolicy.CACHING_DISABLED,
    originRequestPolicy:
      cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
    responseHeadersPolicy: apiSecurityHeaders,
  };

  const trustBehaviorNoCache: cloudfront.BehaviorOptions = {
    origin: trustOrigin,
    viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
    allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
    cachePolicy: cloudfront.CachePolicy.CACHING_DISABLED,
    originRequestPolicy:
      cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
    responseHeadersPolicy: apiSecurityHeaders,
  };

  const trustBehaviorCached: cloudfront.BehaviorOptions = {
    origin: trustOrigin,
    viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
    allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
    cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
    originRequestPolicy:
      cloudfront.OriginRequestPolicy.ALL_VIEWER_EXCEPT_HOST_HEADER,
    responseHeadersPolicy: apiSecurityHeaders,
  };

  const webAcl = new wafv2.CfnWebACL(scope, "WebAcl", {
    name: `${namePrefix}-web-acl`,
    description: `${namePrefix} WAF`,
    scope: "CLOUDFRONT",
    defaultAction: { allow: {} },
    visibilityConfig: {
      cloudWatchMetricsEnabled: true,
      metricName: `${namePrefix}-web-acl`,
      sampledRequestsEnabled: true,
    },
    rules: hostWebAclRules(namePrefix, stage),
  });

  // Pre-build web/dist so SSR assets exist before fromAsset; AppTheorySsrSite
  // wraps the SSR Lambda with an OAC-protected Function URL (AWS_IAM fail-closed).
  ensureWebBuild();
  const webSsrFn = new lambda.Function(scope, "WebSsrFn", {
    functionName: `${namePrefix}-web-ssr`,
    code: lambda.Code.fromAsset(path.join(repoRoot, "web", "dist")),
    handler: "server/face-app.handler",
    runtime: lambda.Runtime.NODEJS_22_X,
    memorySize: 512,
    timeout: cdk.Duration.seconds(15),
    environment: { NODE_OPTIONS: "--enable-source-maps" },
    logRetention: logs.RetentionDays.ONE_WEEK,
  });

  // FaceTheory ISR HTML store: currently holds build-time static hydration
  // sidecars under `_facetheory/data/` (one `<routeId>.json` per shell,
  // generated by web/scripts/build-sidecars.mjs and uploaded by
  // WebSidecarsDeployment below; served at /_facetheory/data/* via the
  // addBehavior below). Future ISR-rendered HTML lives under AppTheory's
  // `isr/` htmlStoreKeyPrefix, and future per-request signed sidecars
  // (createSsrHydrationSidecarStore at /_facetheory/ssr-data/<token>)
  // should use that same store prefix, e.g. `isr/ssr-hydration/...`. The
  // lifecycle rule is therefore scoped only to the ISR prefix; deploy-pruned
  // static sidecars under `_facetheory/data/` intentionally have no TTL.
  const htmlStoreKeyPrefix = "isr";
  const htmlStoreObjectTtl = cdk.Duration.days(stage === "live" ? 7 : 1);
  const htmlStoreBucket = new s3.Bucket(scope, "WebHtmlStoreBucket", {
    bucketName: `${namePrefix}-${cdk.Aws.ACCOUNT_ID}-${cdk.Aws.REGION}-html-store`,
    blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
    enforceSSL: true,
    encryption: s3.BucketEncryption.S3_MANAGED,
    versioned: stage === "live",
    lifecycleRules: [
      {
        id: "ExpireIsrHtmlAndRuntimeSidecars",
        prefix: `${htmlStoreKeyPrefix}/`,
        expiration: htmlStoreObjectTtl,
        noncurrentVersionExpiration: htmlStoreObjectTtl,
      },
    ],
    removalPolicy,
    autoDeleteObjects: stage !== "live",
  });

  // AppTheorySsrSite (M0.12) — canonical AppTheory v1.11.0 composition.
  // Owns the single CloudFront distribution + OAC-signed SSR Lambda
  // origin and the direct S3 Vite asset behaviors.
  const webSite = new AppTheorySsrSite(scope, "WebSite", {
    ssrFunction: webSsrFn,
    mode: AppTheorySsrSiteMode.SSR_ONLY,
    ssrUrlAuthType: lambda.FunctionUrlAuthType.AWS_IAM,
    invokeMode: lambda.InvokeMode.RESPONSE_STREAM,
    assetsBucket: webBucket,
    htmlStoreBucket,
    htmlStoreKeyPrefix,
    responseHeadersPolicy: webSecurityHeaders,
    certificateArn: webCert?.certificateArn,
    domainName: webCert ? webDomainName : undefined,
    // hostedZone intentionally omitted: AppTheorySsrSite would only
    // create an A record; host preserves the existing A + AAAA pair
    // in the `if (webZone && webCert)` block below.
    webAclId: webAcl.attrArn,
    enableLogging: true,
    logsBucket: accessLogsBucket,
    removalPolicy,
    autoDeleteObjects: stage !== "live",
  });
  // Preserve the legacy logical id so the existing CNAME owner updates in place.
  (
    webSite.distribution.node.defaultChild as cloudfront.CfnDistribution
  ).overrideLogicalId("WebDistribution59C46482");

  // /_facetheory/data/* hand-wired to htmlStoreBucket with OAC reads.
  const sidecarOrigin =
    origins.S3BucketOrigin.withOriginAccessControl(htmlStoreBucket);
  webSite.distribution.addBehavior("_facetheory/data/*", sidecarOrigin, {
    viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
    allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
    cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
    responseHeadersPolicy: webSecurityHeaders,
  });
  // Bearer-auth co-origins (existing HTTP API + SSE REST; not Function
  // URLs, so AppTheorySsrSite.bearerFunctionUrlOrigins[] doesn't apply).
  // Attached via addBehavior(); OAC does NOT propagate (M0.13 test).
  webSite.distribution.addBehavior(
    "safe-app*",
    new origins.S3Origin(webBucket, { originAccessIdentity: webOai }),
    {
      viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
      allowedMethods: cloudfront.AllowedMethods.ALLOW_GET_HEAD_OPTIONS,
      cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED,
      responseHeadersPolicy: safeAppSecurityHeaders,
      functionAssociations: [
        {
          function: webSpaRewriteFn,
          eventType: cloudfront.FunctionEventType.VIEWER_REQUEST,
        },
      ],
    },
  );

  // Helper for attaching the existing HTTP API behaviors. Pulls the
  // origin out of the constructed BehaviorOptions object so we can
  // pass the rest of the options through to addBehavior's third arg.
  const attachBearerBehavior = (
    pattern: string,
    behavior: cloudfront.BehaviorOptions,
  ) => {
    webSite.distribution.addBehavior(pattern, behavior.origin, {
      viewerProtocolPolicy: behavior.viewerProtocolPolicy,
      allowedMethods: behavior.allowedMethods,
      cachePolicy: behavior.cachePolicy,
      originRequestPolicy: behavior.originRequestPolicy,
      responseHeadersPolicy: behavior.responseHeadersPolicy,
    });
  };

  attachBearerBehavior("resolve*", trustBehaviorNoCache);
  attachBearerBehavior("health*", trustBehaviorNoCache);
  attachBearerBehavior("api/v1/previews*", trustApiBehavior);
  attachBearerBehavior("api/v1/renders*", trustApiBehavior);
  attachBearerBehavior("api/v1/trust/*", trustApiBehavior);
  attachBearerBehavior("api/v1/publish/jobs*", trustApiBehavior);
  attachBearerBehavior(
    "api/v1/soul/agents/*/update-registration",
    trustApiBehavior,
  );
  attachBearerBehavior("api/v1/ai/*", trustApiBehavior);
  attachBearerBehavior("api/v1/budget/debit", trustApiBehavior);
  attachBearerBehavior(
    "api/v1/soul/agents/register/*/mint-conversation*",
    apiSseBehavior,
  );
  attachBearerBehavior(
    "api/v1/soul/agents/*/mint-conversation*",
    apiSseBehavior,
  );
  // Lesser instance-key hosted-genesis uses JSON HostedGenesisSession authority, not legacy SSE/queue authority.
  attachBearerBehavior(
    "api/v1/soul/instance/agents/register/*/mint-conversation*",
    apiBehavior,
  );

  attachBearerBehavior("api/*", apiBehavior);
  attachBearerBehavior("auth/*", apiBehavior);
  attachBearerBehavior("webhooks/*", apiBehavior);
  attachBearerBehavior("setup/status", apiBehavior);
  attachBearerBehavior("setup/bootstrap/*", apiBehavior);
  attachBearerBehavior("setup/admin", apiBehavior);
  attachBearerBehavior("setup/finalize", apiBehavior);

  attachBearerBehavior(".well-known/*", trustBehaviorCached);
  attachBearerBehavior("attestations", trustBehaviorNoCache);
  attachBearerBehavior("attestations/*", trustBehaviorCached);

  // Back-compat alias so downstream env-wiring / route53 / outputs /
  // bucket-deployment blocks below keep referencing `webDistribution`
  // without diff churn.
  const webDistribution = webSite.distribution;

  const publicBaseURL = webCert
    ? `https://${webDomainName}`
    : `https://${webDistribution.distributionDomainName}`;
  controlPlaneFunction.addEnvironment("PUBLIC_BASE_URL", publicBaseURL);
  controlPlaneFunction.addEnvironment(
    "SOUL_EMAIL_INBOUND_DOMAIN",
    soulEmailInboundDomain,
  );
  trustFunction.addEnvironment("PUBLIC_BASE_URL", publicBaseURL);

  new s3deploy.BucketDeployment(scope, "WebDeployment", {
    destinationBucket: webBucket,
    sources: [
      s3deploy.Source.asset(path.join(repoRoot, "web"), {
        bundling: {
          image: cdk.DockerImage.fromRegistry("node:24-bookworm"),
          local: {
            tryBundle(outputDir: string) {
              if (!shouldUseLocalWebBundling()) {
                return false;
              }

              const webDir = path.join(repoRoot, "web");
              const tempDir = fs.mkdtempSync(
                path.join(
                  path.join(repoRoot, "cdk", ".build"),
                  "web-bundle-",
                ),
              );
              try {
                fs.cpSync(webDir, tempDir, {
                  recursive: true,
                  filter: (src) => {
                    const rel = path.relative(webDir, src);
                    if (rel === "") return true;
                    return (
                      !rel.startsWith("node_modules") &&
                      !rel.startsWith("dist")
                    );
                  },
                });
                execFileSync("npm", ["ci"], {
                  cwd: tempDir,
                  stdio: "inherit",
                });
                execFileSync("npm", ["run", "build"], {
                  cwd: tempDir,
                  stdio: "inherit",
                });
                fs.cpSync(path.join(tempDir, "dist"), outputDir, {
                  recursive: true,
                });
                return true;
              } catch {
                return false;
              } finally {
                fs.rmSync(tempDir, { recursive: true, force: true });
              }
            },
          },
          command: [
            "bash",
            "-c",
            "export HOME=/tmp npm_config_cache=/tmp/.npm NPM_CONFIG_CACHE=/tmp/.npm && rm -rf /tmp/webbuild && mkdir -p /tmp/webbuild /tmp/.npm && cp -R /asset-input/. /tmp/webbuild && cd /tmp/webbuild && rm -rf node_modules dist && npm ci --cache /tmp/.npm && npm run build && cp -r dist/* /asset-output/",
          ],
        },
      }),
    ],
    distribution: webDistribution,
    distributionPaths: ["/*"],
  });

  // Deploy the static FaceTheory hydration sidecars to htmlStoreBucket
  // at key prefix `_facetheory/data/` so CloudFront's /_facetheory/data/*
  // behavior (which forwards the full URL path to S3 without OriginPath
  // rewrite) finds each `<routeId>.json` at the expected key. Sources
  // are produced by `web/scripts/build-sidecars.mjs` and bundled into
  // `dist/sidecars/` during `npm run build` (see WebDeployment above).
  new s3deploy.BucketDeployment(scope, "WebSidecarsDeployment", {
    destinationBucket: htmlStoreBucket,
    destinationKeyPrefix: "_facetheory/data",
    sources: [
      s3deploy.Source.asset(
        path.join(repoRoot, "web", "dist", "sidecars"),
      ),
    ],
    distribution: webDistribution,
    distributionPaths: ["/_facetheory/data/*"],
    prune: true,
  });

  if (webZone && webCert) {
    new route53.ARecord(scope, "WebAliasA", {
      zone: webZone,
      recordName: stage === "live" ? undefined : stage,
      target: route53.RecordTarget.fromAlias(
        new route53Targets.CloudFrontTarget(webDistribution),
      ),
    });
    new route53.AaaaRecord(scope, "WebAliasAAAA", {
      zone: webZone,
      recordName: stage === "live" ? undefined : stage,
      target: route53.RecordTarget.fromAlias(
        new route53Targets.CloudFrontTarget(webDistribution),
      ),
    });
  }


  return {
    publicBaseUrl: publicBaseURL,
    webDistributionDomainName: webDistribution.distributionDomainName,
    webUrl: webCert
      ? `https://${webDomainName}`
      : `https://${webDistribution.distributionDomainName}`,
  };
}
