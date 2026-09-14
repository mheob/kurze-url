import { PanelLeftIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { Button } from './ui/button';
import { useSidebar } from './ui/sidebar';

/**
 * A translated stand-in for `ui/sidebar.tsx`'s generated `SidebarTrigger`.
 * That component hardcodes its `sr-only` label as the English literal
 * "Toggle Sidebar" — invisible to sighted users but still a text node, so
 * `e2e/i18n.spec.ts`'s crawl (which reads every visible-per-Playwright
 * string, `sr-only` spans included) flagged it as a string that never
 * changes with the language, even with an `aria-label` layered on top: an
 * `aria-label` overrides the accessible *name* but does not remove the span's
 * text from the rendered page. `ui/**` is generator output and must never be
 * hand-edited (see `generated.config.ts`) — a hand edit survives only until
 * the next `shadcn add` — so this wraps it instead of patching it in place.
 *
 * Same `Button` variant, size and `data-sidebar`/`data-slot` attributes as
 * the original, so nothing about the rendered appearance changes; only the
 * label now comes from the `nav.toggleSidebar` catalogue entry. That label
 * alone already gives the button its accessible name (the "name from
 * content" step of the accessible-name computation includes `sr-only` text),
 * so callers should not also pass an `aria-label` — that would just be a
 * second copy of the same string to keep in sync.
 *
 * @returns The rendered sidebar-toggle button.
 */
export function SidebarTrigger(): React.JSX.Element {
	const { t } = useTranslation();
	const { toggleSidebar } = useSidebar();

	return (
		<Button
			data-sidebar="trigger"
			data-slot="sidebar-trigger"
			onClick={toggleSidebar}
			size="icon-sm"
			variant="ghost"
		>
			<PanelLeftIcon />
			<span className="sr-only">{t('nav.toggleSidebar')}</span>
		</Button>
	);
}
