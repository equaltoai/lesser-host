import js from '@eslint/js';
import globals from 'globals';
import svelte from 'eslint-plugin-svelte';
import svelteParser from 'svelte-eslint-parser';
import tseslint from 'typescript-eslint';

const jsTsFiles = ['**/*.js', '**/*.ts'];

export default [
	{
		ignores: [
			'dist/**',
			'node_modules/**',
			'src/lib/greater/**',
			'src/lib/primitives/**',
			'src/lib/utils/**',
			'src/lib/types/**',
		],
	},
	...svelte.configs['flat/recommended'],
	{
		files: ['**/*.svelte'],
		languageOptions: {
			parser: svelteParser,
			parserOptions: {
				parser: tseslint.parser,
				extraFileExtensions: ['.svelte'],
			},
		},
	},
	{
		files: jsTsFiles,
		...js.configs.recommended,
	},
	...tseslint.configs.recommended.map((config) => ({
		...config,
		files: jsTsFiles,
	})),
	{
		languageOptions: {
			globals: {
				...globals.browser,
				...globals.node,
			},
		},
		rules: {
			'no-console': ['warn', { allow: ['warn', 'error'] }],
		},
	},
];
