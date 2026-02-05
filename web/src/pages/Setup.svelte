<script lang="ts">
	import { navigate } from 'src/lib/router';
	import { Button, Container, Heading, Text } from 'src/lib/ui';
</script>

<Container size="lg" gutter="lg">
	<div class="setup">
		<header class="setup__header">
			<div>
				<Heading level={1}>Setup</Heading>
				<Text color="secondary">Wizard UI ships in FE1.</Text>
			</div>
			<Button variant="ghost" onclick={() => navigate('/')}>Home</Button>
		</header>

		<Text>
			This route exists to avoid UI/API path conflicts and to provide a place for the FE1 setup wizard.
		</Text>
	</div>
</Container>

<style>
	.setup {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		padding: var(--gr-spacing-scale-12) 0;
	}

	.setup__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}
</style>
