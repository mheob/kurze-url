import { createFileRoute } from '@tanstack/react-router';

function Home() {
	return <main data-testid="home" />;
}

export const Route = createFileRoute('/')({
	component: Home,
});
