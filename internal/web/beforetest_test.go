package web

import (
	"testing"
)

func TestPolicyRules(t *testing.T) {
	tests := []struct {
		name, text string
		want       []testRule
	}{
		{"nothing", "Be nice. Report bugs.", nil},
		{"header and email from a table", "|Custom Request Header| *X-Bug-Bounty: <username>-acme* |\n| Accounts | Register with your `@wearehackerone.com` email address |",
			[]testRule{
				{Kind: "header", Title: "X-Bug-Bounty: <username>-acme", Detail: "Send this header with every request.", Copy: "X-Bug-Bounty: <username>-acme", FromPolicy: true},
				{Kind: "email", Title: "Sign up with your @wearehackerone.com address", Detail: "For test accounts.", FromPolicy: true},
			}},
		{"user agent and rate", "Set your User-Agent: `Intigriti-<username>-Bug-Bounty` and keep under 5 requests per second.",
			[]testRule{
				{Kind: "agent", Title: "User-Agent: Intigriti-<username>-Bug-Bounty", Detail: "Send it with every request.", Copy: "Intigriti-<username>-Bug-Bounty", FromPolicy: true},
				{Kind: "rate", Title: "At most 5 requests a second", Detail: "For automated tools.", FromPolicy: true},
			}},
		{"security headers aren't asked for", "Missing X-Frame-Options: DENY isn't a bug.", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policyRules(tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("rule %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
