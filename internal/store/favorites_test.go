package store

import "testing"

func TestValidFavorite(t *testing.T) {
	for _, tc := range []struct {
		name, kind, value string
		want              bool
	}{
		{"neighbor", "neighbor", "12", true}, {"rig", "gespann", "1", true},
		{"task", "task", "Mähen", true}, {"zero", "neighbor", "0", false},
		{"negative", "neighbor", "-1", false}, {"noncanonical", "neighbor", "01", false},
		{"overflow", "neighbor", "9223372036854775808", false}, {"unknown", "users", "1", false},
		{"multiline", "task", "a\nb", false}, {"blank", "task", " ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validFavorite(FavoriteChange{Kind: tc.kind, Value: tc.value}); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
