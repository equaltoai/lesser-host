<script lang="ts">
	import { linkProps } from 'src/lib/router';
	import { Container, Heading, Link, Text } from 'src/lib/ui';
</script>

<Container size="lg" gutter="lg">
	<div class="not-found">
		<Heading level={1}>Not found</Heading>
		<Text color="secondary">This route does not exist.</Text>
		<div class="not-found__actions">
			<Link {...linkProps('/')} variant="default">Home</Link>
		</div>
	</div>
</Container>

<style>
	.not-found {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-4);
		padding: var(--gr-spacing-scale-12) 0;
	}

	.not-found__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
	}
</style>
