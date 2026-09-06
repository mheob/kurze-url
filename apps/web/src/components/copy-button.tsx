import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from './ui/button';

/**
 * Confirms via `aria-live`, not only a colour or icon change: a screen reader
 * never sees a colour change, and accessibility is a CI gate in this project.
 * The confirmation clears on the next click's writeText call implicitly —
 * see the `useState` reset below — rather than on a timer, so a screen
 * reader always hears the confirmation for the copy it actually announces.
 *
 * `label` is an optional accessible-name override, for a caller that renders
 * more than one `CopyButton` in the same row for different values (two DNS
 * record values side by side, for example) — the visible text stays "Copy"
 * either way, since sighted users can already tell the buttons apart by
 * position, but `aria-label` gives anyone tabbing through them a distinct
 * name per button instead of two indistinguishable "Copy" controls.
 */
export function CopyButton({
	label,
	value,
}: {
	readonly label?: string;
	readonly value: string;
}): React.JSX.Element {
	const { t } = useTranslation();
	const [copied, setCopied] = useState(false);

	return (
		<>
			<Button
				aria-label={label}
				onClick={() => {
					setCopied(false);
					void navigator.clipboard.writeText(value).then(() => setCopied(true));
				}}
				type="button"
			>
				{t('links.copy')}
			</Button>
			<span aria-live="polite">{copied ? t('links.copied') : ''}</span>
		</>
	);
}
