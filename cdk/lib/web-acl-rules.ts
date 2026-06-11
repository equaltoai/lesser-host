import * as wafv2 from 'aws-cdk-lib/aws-wafv2';

export const NO_USER_AGENT_MANAGED_LABEL = 'awswaf:managed:aws:core-rule-set:NoUserAgent_Header';

export function hostWebAclRules(namePrefix: string, stage: string): wafv2.CfnWebACL.RuleProperty[] {
	return [
		{
			name: 'AWSManagedRulesCommonRuleSet',
			priority: 0,
			overrideAction: { none: {} },
			statement: {
				managedRuleGroupStatement: {
					vendorName: 'AWS',
					name: 'AWSManagedRulesCommonRuleSet',
					ruleActionOverrides: [
						{
							// ENS CCIP-Read clients are not browsers and some common
							// clients (including default Node ethers FetchRequest) send
							// no User-Agent. Count the managed no-UA signal here, then
							// enforce our own route-scoped block below so only /resolve
							// receives the interoperability exception.
							name: 'NoUserAgent_HEADER',
							actionToUse: { count: {} },
						},
					],
				},
			},
			visibilityConfig: {
				cloudWatchMetricsEnabled: true,
				metricName: `${namePrefix}-waf-common`,
				sampledRequestsEnabled: true,
			},
		},
		{
			name: 'BlockNoUserAgentExceptResolve',
			priority: 1,
			action: { block: {} },
			statement: {
				andStatement: {
					statements: [
						{
							labelMatchStatement: {
								scope: 'LABEL',
								key: NO_USER_AGENT_MANAGED_LABEL,
							},
						},
						{
							notStatement: {
								statement: {
									byteMatchStatement: {
										fieldToMatch: { uriPath: {} },
										positionalConstraint: 'EXACTLY',
										searchString: '/resolve',
										textTransformations: [{ priority: 0, type: 'NONE' }],
									},
								},
							},
						},
					],
				},
			},
			visibilityConfig: {
				cloudWatchMetricsEnabled: true,
				metricName: `${namePrefix}-waf-no-user-agent`,
				sampledRequestsEnabled: true,
			},
		},
		{
			name: 'AWSManagedRulesKnownBadInputsRuleSet',
			priority: 2,
			overrideAction: { none: {} },
			statement: {
				managedRuleGroupStatement: {
					vendorName: 'AWS',
					name: 'AWSManagedRulesKnownBadInputsRuleSet',
				},
			},
			visibilityConfig: {
				cloudWatchMetricsEnabled: true,
				metricName: `${namePrefix}-waf-bad-inputs`,
				sampledRequestsEnabled: true,
			},
		},
		{
			name: 'IpRateLimit',
			priority: 3,
			action: { block: {} },
			statement: {
				rateBasedStatement: {
					limit: stage === 'live' ? 2000 : 5000,
					aggregateKeyType: 'IP',
				},
			},
			visibilityConfig: {
				cloudWatchMetricsEnabled: true,
				metricName: `${namePrefix}-waf-ip-rate-limit`,
				sampledRequestsEnabled: true,
			},
		},
	];
}
