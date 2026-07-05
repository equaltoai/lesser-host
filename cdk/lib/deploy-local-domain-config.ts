import * as fs from 'node:fs';
import * as path from 'node:path';

export interface WebDomainConfig {
	rootDomain: string;
	hostedZoneId: string;
	hostedZoneName: string;
}

const deployLocalConfigRelativePath = 'app-theory/deploy.local.json';
const deployLocalConfigExampleRelativePath = 'app-theory/deploy.local.json.example';
const examplePlaceholderPattern = /<[^>]*YOUR[^>]*>|YOUR_[A-Z0-9_]+/i;

function defaultDomainConfigPath(): string {
	return path.resolve(process.cwd(), '..', deployLocalConfigRelativePath);
}

function deployLocalConfigInstructions(stage: string, configPath: string): string {
	return `Expected ${deployLocalConfigRelativePath} at ${configPath}. Copy ${deployLocalConfigExampleRelativePath} to ${deployLocalConfigRelativePath} and fill domain.${stage}.{rootDomain,hostedZoneId,hostedZoneName}. No fallback to cdk.json context, environment variables, Route53 lookup, or defaults is available.`;
}

function failDeployLocalDomainConfig(stage: string, configPath: string, message: string): never {
	throw new Error(`Deploy domain config error: ${message}. ${deployLocalConfigInstructions(stage, configPath)}`);
}

function asConfigRecord(value: unknown): Record<string, unknown> | undefined {
	return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function readRequiredDomainNameField(
	stage: string,
	configPath: string,
	stageConfig: Record<string, unknown>,
	fieldName: 'rootDomain' | 'hostedZoneName',
): string {
	const raw = stageConfig[fieldName];
	const value = typeof raw === 'string' ? raw.trim().replace(/\.$/, '').toLowerCase() : '';
	if (value === '' || examplePlaceholderPattern.test(value)) {
		failDeployLocalDomainConfig(stage, configPath, `domain.${stage}.${fieldName} is required`);
	}
	if (
		value.includes('/') ||
		value.includes(':') ||
		value.startsWith('*.') ||
		!/^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$/.test(value) ||
		!value.includes('.')
	) {
		failDeployLocalDomainConfig(stage, configPath, `domain.${stage}.${fieldName} must be a DNS name, got ${JSON.stringify(value)}`);
	}
	return value;
}

function readHostedZoneId(stage: string, configPath: string, stageConfig: Record<string, unknown>): string {
	const raw = stageConfig.hostedZoneId;
	const value = typeof raw === 'string' ? raw.trim() : '';
	if (value === '' || examplePlaceholderPattern.test(value)) {
		failDeployLocalDomainConfig(stage, configPath, `domain.${stage}.hostedZoneId is required`);
	}
	if (!/^Z[A-Z0-9]{4,32}$/.test(value)) {
		failDeployLocalDomainConfig(stage, configPath, `domain.${stage}.hostedZoneId must be an AWS Route53 hosted zone id such as Z123EXAMPLE, got ${JSON.stringify(value)}`);
	}
	return value;
}

export function readWebDomainConfig(stage: string, configPath = defaultDomainConfigPath()): WebDomainConfig {
	const stageKey = stage.trim().toLowerCase();
	if (stageKey === '') {
		failDeployLocalDomainConfig('lab', configPath, 'stage is required to select a deploy-local domain entry');
	}

	let raw: string;
	try {
		raw = fs.readFileSync(configPath, 'utf8');
	} catch (err) {
		const code = (err as NodeJS.ErrnoException).code;
		if (code === 'ENOENT') {
			failDeployLocalDomainConfig(stageKey, configPath, `missing ${deployLocalConfigRelativePath}`);
		}
		failDeployLocalDomainConfig(stageKey, configPath, `failed reading ${deployLocalConfigRelativePath}: ${String(err)}`);
	}

	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch (err) {
		failDeployLocalDomainConfig(stageKey, configPath, `invalid JSON in ${deployLocalConfigRelativePath}: ${String(err)}`);
	}

	const root = asConfigRecord(parsed);
	const domain = asConfigRecord(root?.domain);
	if (!domain) {
		failDeployLocalDomainConfig(stageKey, configPath, 'missing domain map');
	}
	const stageConfig = asConfigRecord(domain[stageKey]);
	if (!stageConfig) {
		failDeployLocalDomainConfig(stageKey, configPath, `missing domain.${stageKey} entry`);
	}

	return {
		rootDomain: readRequiredDomainNameField(stageKey, configPath, stageConfig, 'rootDomain'),
		hostedZoneId: readHostedZoneId(stageKey, configPath, stageConfig),
		hostedZoneName: readRequiredDomainNameField(stageKey, configPath, stageConfig, 'hostedZoneName'),
	};
}
