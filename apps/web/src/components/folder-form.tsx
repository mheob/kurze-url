import { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { normalizeFolderName } from '../lib/folders';
import { Button } from './ui/button';
import { Field, FieldError, FieldLabel } from './ui/field';
import { Input } from './ui/input';

interface FolderFormProps {
	readonly autoFocus?: boolean;
	readonly error?: string;
	readonly initialName?: string;
	readonly label: string;
	readonly onCancel?: () => void;
	readonly onSubmit: (name: string) => void;
	readonly submitLabel: string;
}

/**
 * One name field, used both to create a folder and, inline, to rename one.
 * The client applies the API's name rule first, so a blank or over-long name
 * never costs a request, and it shows the same words the API's 422 maps to.
 *
 * @param props - The form's props.
 * @param props.autoFocus - Focus the field on mount, for the inline rename.
 * @param props.error - A server-side failure to show on the field.
 * @param props.initialName - The current name, when renaming.
 * @param props.label - The field's visible label.
 * @param props.onCancel - Present for the inline rename; Escape and the cancel button call it.
 * @param props.onSubmit - Called with the normalized name.
 * @param props.submitLabel - The submit button's text.
 * @returns The form.
 */
export function FolderForm({
	autoFocus = false,
	error,
	initialName = '',
	label,
	onCancel,
	onSubmit,
	submitLabel,
}: FolderFormProps): React.JSX.Element {
	const { t } = useTranslation();
	const id = useId();
	const errorId = useId();
	const input = useRef<HTMLInputElement>(null);
	const [value, setValue] = useState(initialName);
	const [localError, setLocalError] = useState<string | undefined>(undefined);
	const message = localError ?? error;

	useEffect(() => {
		if (autoFocus) input.current?.focus();
	}, [autoFocus]);

	return (
		// Escape-to-cancel on the whole form is standard for an inline editor
		// like this one; the key handler doesn't turn the form into a custom
		// widget needing its own role, so the non-interactive-element rule is
		// a false positive here rather than a real semantics problem.
		// oxlint-disable-next-line jsx-a11y/no-noninteractive-element-interactions
		<form
			noValidate
			onKeyDown={(event: Readonly<{ key: string }>) => {
				if (event.key === 'Escape' && onCancel) onCancel();
			}}
			onSubmit={(event: Readonly<{ preventDefault: () => void }>) => {
				event.preventDefault();
				const name = normalizeFolderName(value);
				if (name === undefined) {
					setLocalError(t('folders.nameInvalid'));
					return;
				}
				setLocalError(undefined);
				onSubmit(name);
			}}
		>
			<Field data-invalid={message !== undefined}>
				<FieldLabel htmlFor={id}>{label}</FieldLabel>
				<Input
					aria-describedby={message === undefined ? undefined : errorId}
					aria-invalid={message === undefined ? undefined : true}
					id={id}
					onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
						setValue(event.target.value);
					}}
					ref={input}
					value={value}
				/>
				{message === undefined ? null : <FieldError id={errorId}>{message}</FieldError>}
			</Field>
			<Button type="submit">{submitLabel}</Button>
			{onCancel ? (
				<Button onClick={onCancel} type="button" variant="ghost">
					{t('folders.cancel')}
				</Button>
			) : null}
		</form>
	);
}
