package ajv

import "testing"

func TestResolveMatchesFastURI(t *testing.T) {
	cases := []struct{ base, rel, want string }{
		{"", "https://example.com/a.json", "https://example.com/a.json"},
		{"https://example.com/a/b.json", "c.json", "https://example.com/a/c.json"},
		{"https://example.com/a/b.json", "#/definitions/x", "https://example.com/a/b.json#/definitions/x"},
		{"https://example.com/a/b.json", "../c.json#foo", "https://example.com/c.json#foo"},
		{"", "#foo", "#foo"},
		{"", "#/$defs/a", "#/$defs/a"},
		{"http://x.y/s", "http://x.y:80/t", "http://x.y:80/t"},
		{"https://Example.COM/A", "b", "https://example.com/b"},
		{"urn:example:foo", "#bar", "urn:example:foo#bar"},
		{"", "urn:uuid:5D1C3C2A-9B5A-4b0b-8a8e-3f2b0a6c1d2e", "urn:uuid:5D1C3C2A-9B5A-4b0b-8a8e-3f2b0a6c1d2e"},
		{"https://e.com/a b", "c", "https://e.com/c"},
		{"tag:x,2020:a", "b", "tag:b"},
		{"https://e.com/%7euser/", "x", "https://e.com/~user/x"},
		{"", "node", "node"},
		{"https://e.com/root.json", "/abs#frag", "https://e.com/abs#frag"},
		{"https://e.com/x?q=1", "?r=2", "https://e.com/x?r=2"},
		{"http://[::1]:8080/a", "b", "http://[::1]:8080/b"},
		{"https://e.com/a", "//other.org/b", "https://other.org/b"},
		{"file:///tmp/a.json", "b.json", "file:///tmp/b.json"},
		{"https://e.com/a#", "", "https://e.com/a"},
	}
	for _, c := range cases {
		got, err := uriResolve(c.base, c.rel)
		if err != nil || got != c.want {
			t.Errorf("resolve(%q, %q) = %q, %v; want %q", c.base, c.rel, got, err, c.want)
		}
	}
}
