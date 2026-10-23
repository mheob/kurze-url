import { describe, expect, it } from 'vite-plus/test';

import { isSafeNext } from './safe-next';

describe(isSafeNext, () => {
	it('accepts the consent path with its query', () => {
		expect(isSafeNext('/oauth/consent?authorization_id=abc')).toBe(true);
	});

	it('accepts the extra parameters a consent link may carry', () => {
		expect(isSafeNext('/oauth/consent?authorization_id=0b5c1d2e-3f4a&foo=bar')).toBe(true);
	});

	// The lone-surrogate refusal below must not reach a surrogate pair, which is one ordinary
	// character: only the unpaired half is a problem.
	it('accepts a character outside the basic plane, which is a valid surrogate pair', () => {
		expect(isSafeNext('/oauth/consent?authorization_id=abc&label=\u{1F600}')).toBe(true);
	});

	// Each row is a way to make a login link send the visitor somewhere other
	// than this app's own consent page. Every one must fall back to `/`.
	it.each([
		['an empty string', ''],
		['a protocol-relative URL', '//evil.example'],
		['a protocol-relative URL that ends in the consent path', '//evil.example/oauth/consent?x'],
		['a backslash that browsers read as a slash', String.raw`/\evil.example`],
		['a backslash inside the query', String.raw`/oauth/consent?x\y`],
		['an absolute URL on another origin', 'https://evil.example/oauth/consent?x'],
		['an absolute URL on this origin', 'https://kurze-url.app/oauth/consent?x'],
		['a longer path segment that shares the prefix', '/oauth/consentx?authorization_id=1'],
		['a deeper path under the consent path', '/oauth/consent/evil?authorization_id=1'],
		['a different case', '/OAuth/Consent?authorization_id=1'],
		['the consent path without a query', '/oauth/consent'],
		['the root', '/'],
		['another path of this app', '/links'],
		['a path with no leading slash', 'oauth/consent?authorization_id=1'],
		['leading whitespace', ' /oauth/consent?authorization_id=1'],
		['a leading newline', '\n/oauth/consent?authorization_id=1'],
		['a trailing newline', '/oauth/consent?authorization_id=1\n'],
		['a carriage return and line feed', '/oauth/consent?authorization_id=1\r\nSet-Cookie: a=b'],
		['a tab, which URL parsers strip', '/oauth/consent?authorization_id=\t1'],
		['a NUL byte', '/oauth/consent?authorization_id=1\u0000'],
		['a DEL character', '/oauth/consent?authorization_id=1\u007F'],
		['an encoded CRLF in the query', '/oauth/consent?authorization_id=1%0d%0aSet-Cookie:a=b'],
		['an upper-case encoded CRLF in the query', '/oauth/consent?authorization_id=1%0D%0A'],
		['an encoded CRLF in front of the path', '%0d%0a/oauth/consent?authorization_id=1'],
		// `encodeURIComponent` throws a URIError on a lone surrogate, which would turn
		// the login's callback URL into a 500 instead of a refusal.
		['a lone surrogate, which cannot be percent-encoded', '/oauth/consent?authorization_id=\uD800'],
	])('refuses %s', (_name, next) => {
		expect(isSafeNext(next)).toBe(false);
	});

	it.each([
		['undefined', undefined],
		['null', null],
		['a number', 42],
		['an object', { next: '/oauth/consent?authorization_id=1' }],
		['an array holding a valid path', ['/oauth/consent?authorization_id=1']],
	])('refuses %s, which is not a string', (_name: string, next: unknown) => {
		expect(isSafeNext(next)).toBe(false);
	});
});
