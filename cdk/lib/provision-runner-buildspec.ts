import * as fs from 'fs';
import * as path from 'path';

// When executed from dist/lib (tsc output) __dirname ends with dist/lib;
// when executed directly via tsx it is lib.  Resolve to the source tree either way.
const SCRIPTS_DIR = __dirname.endsWith(path.join('dist', 'lib'))
	? path.resolve(__dirname, '..', '..', 'lib', 'provision-runner')
	: path.join(__dirname, 'provision-runner');

function readScript(name: string): string {
	return fs.readFileSync(path.join(SCRIPTS_DIR, name), 'utf8');
}

/**
 * Assembles the build-phase shell script from the individual .sh files.
 *
 * At CDK synth time the separate scripts are concatenated into a single string
 * that CodeBuild executes as inline commands.  The .sh files under
 * cdk/lib/provision-runner/ are the canonical, lintable source of truth.
 */
function assembleBuildScript(): string {
	const helpers = readScript('helpers.sh');
	const body = readScript('build-lesser-body.sh');
	const lesser = readScript('build-lesser.sh');
	const mcp = readScript('build-lesser-mcp.sh');
	const dispatcher = readScript('build.sh');

	// Strip shebangs — CodeBuild already runs under bash.
	const stripShebang = (s: string) => s.replace(/^#!\/usr\/bin\/env bash\n/, '');
	const stripAnyShebang = (s: string) => s.replace(/^#!.*\n/, '');

	// Replace ### INLINE: <file> ### markers with the actual script content.
	//
	// Use replacement callbacks so shell literals containing `$'`, `$&`, or
	// similar sequences are inserted verbatim. Passing raw script text as the
	// replacement string lets JavaScript's String.replace expand replacement
	// tokens, which can corrupt the rendered CodeBuild shell script.
	let assembled = dispatcher;
	assembled = assembled.replace(/^.*### INLINE: build-lesser-body\.sh ###.*$/m, () => stripAnyShebang(body));
	assembled = assembled.replace(/^.*### INLINE: build-lesser\.sh ###.*$/m, () => stripAnyShebang(lesser));
	assembled = assembled.replace(/^.*### INLINE: build-lesser-mcp\.sh ###.*$/m, () => stripAnyShebang(mcp));

	return [stripShebang(helpers), stripShebang(assembled)].join('\n');
}

export function renderProvisionRunnerPreBuildCommands(): string {
	return readScript('prebuild.sh').replace(/^#!\/usr\/bin\/env bash\n/, '');
}

export function renderProvisionRunnerBuildCommands(): string {
	return assembleBuildScript();
}
