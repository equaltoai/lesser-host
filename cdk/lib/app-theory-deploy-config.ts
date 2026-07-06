import * as fs from 'node:fs';
import * as path from 'node:path';

export interface WebDomainConfig {
	rootDomain: string;
	hostedZoneId: string;
	hostedZoneName: string;
}

const appConfigRelativePath = 'app-theory/app.json';
const webDomainConfigPath = 'lesserHost.webDomain';
const examplePlaceholderPattern = /<[^>]+>|YOUR_[A-Z0-9_]+|\bTODO\b|\bTBD\b|\bPLACEHOLDER\b|\bCHANGEME\b|EXAMPLE/i;

function defaultAppConfigPath(): string {
	return path.resolve(process.cwd(), '..', appConfigRelativePath);
}

function appConfigInstructions(stage: string, configPath: string): string {
	return `Expected ${appConfigRelativePath} at ${configPath} to define ${webDomainConfigPath}.${stage}.{rootDomain,hostedZoneId,hostedZoneName}. This is the AppTheory app-up source of truth for lesser-host web domain config; no fallback to cdk.json context, environment variables, Route53 lookup, sidecar files, or CloudFront defaults is available.`;
}

function failAppTheoryDeployConfig(stage: string, configPath: string, message: string): never {
	throw new Error(`AppTheory deploy config error: ${message}. ${appConfigInstructions(stage, configPath)}`);
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
		failAppTheoryDeployConfig(stage, configPath, `${webDomainConfigPath}.${stage}.${fieldName} is required`);
	}
	if (
		value.includes('/') ||
		value.includes(':') ||
		value.startsWith('*.') ||
		!/^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$/.test(value) ||
		!value.includes('.')
	) {
		failAppTheoryDeployConfig(stage, configPath, `${webDomainConfigPath}.${stage}.${fieldName} must be a DNS name`);
	}
	return value;
}

function readHostedZoneId(stage: string, configPath: string, stageConfig: Record<string, unknown>): string {
	const raw = stageConfig.hostedZoneId;
	const value = typeof raw === 'string' ? raw.trim() : '';
	if (value === '' || examplePlaceholderPattern.test(value)) {
		failAppTheoryDeployConfig(stage, configPath, `${webDomainConfigPath}.${stage}.hostedZoneId is required`);
	}
	if (!/^Z[A-Z0-9]{4,32}$/.test(value)) {
		failAppTheoryDeployConfig(stage, configPath, `${webDomainConfigPath}.${stage}.hostedZoneId must be an AWS Route53 hosted zone id`);
	}
	return value;
}

export function readWebDomainConfig(stage: string, configPath = defaultAppConfigPath()): WebDomainConfig {
	const stageKey = stage.trim().toLowerCase();
	if (stageKey === '') {
		failAppTheoryDeployConfig('lab', configPath, 'stage is required to select an AppTheory app web-domain entry');
	}

	let raw: string;
	try {
		raw = fs.readFileSync(configPath, 'utf8');
	} catch (err) {
		const code = (err as NodeJS.ErrnoException).code;
		if (code === 'ENOENT') {
			failAppTheoryDeployConfig(stageKey, configPath, `missing ${appConfigRelativePath}`);
		}
		failAppTheoryDeployConfig(stageKey, configPath, `failed reading ${appConfigRelativePath}: ${String(err)}`);
	}

	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch (err) {
		failAppTheoryDeployConfig(stageKey, configPath, `invalid JSON in ${appConfigRelativePath}: ${String(err)}`);
	}

	const root = asConfigRecord(parsed);
	const lesserHost = asConfigRecord(root?.lesserHost);
	const webDomain = asConfigRecord(lesserHost?.webDomain);
	if (!webDomain) {
		failAppTheoryDeployConfig(stageKey, configPath, `missing ${webDomainConfigPath} map`);
	}
	const stageConfig = asConfigRecord(webDomain[stageKey]);
	if (!stageConfig) {
		failAppTheoryDeployConfig(stageKey, configPath, `missing ${webDomainConfigPath}.${stageKey} entry`);
	}

	return {
		rootDomain: readRequiredDomainNameField(stageKey, configPath, stageConfig, 'rootDomain'),
		hostedZoneId: readHostedZoneId(stageKey, configPath, stageConfig),
		hostedZoneName: readRequiredDomainNameField(stageKey, configPath, stageConfig, 'hostedZoneName'),
	};
}
