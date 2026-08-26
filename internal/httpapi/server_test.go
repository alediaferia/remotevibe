package httpapi

import "testing"

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Owner/My.Repo":          "owner-my-repo",
		"alediaferia/remotevibe": "alediaferia-remotevibe",
		"a/_weird__name_":        "a-weird-name",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}
