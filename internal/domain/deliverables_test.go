package domain

import (
	"errors"
	"strings"
	"testing"
)

const sha1 = "0123456789abcdef0123456789abcdef01234567"
const sha2 = "fedcba9876543210fedcba9876543210fedcba98"

func TestExtractLastJSON(t *testing.T) {
	cases := []struct {
		name, in, want string
		err            bool
	}{
		{"fence", "texte\n```json\n{\"a\":1}\n```\nfin", `{"a":1}`, false},
		{"last fence wins", "```json\n{\"a\":1}\n```\n```json\n{\"b\":2}\n```", `{"b":2}`, false},
		{"raw", `bla {"a":{"b":1}} bla`, `{"a":{"b":1}}`, false},
		{"raw last", `{"x":1} puis {"y":{"z":[1,2]}} fin`, `{"y":{"z":[1,2]}}`, false},
		{"braces in prose", `voir {pas du json} puis {"ok":true}`, `{"ok":true}`, false},
		{"none", "aucun json ici", "", true},
		{"fence not object", "```json\n[1,2]\n```", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ExtractLastJSON(c.in)
			if c.err {
				if err == nil {
					t.Fatalf("erreur attendue, obtenu %s", got)
				}
				return
			}
			if err != nil || string(got) != c.want {
				t.Fatalf("obtenu %q, %v ; attendu %q", got, err, c.want)
			}
		})
	}
}

func TestDecodeStrict(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"ok", `{"summary":"s","user_stories":["u"],"acceptance_criteria":["a"],"open_questions":[]}`, true},
		{"unknown key", `{"summary":"s","user_stories":["u"],"acceptance_criteria":["a"],"open_questions":[],"x":1}`, false},
		{"missing key", `{"summary":"s","user_stories":["u"],"acceptance_criteria":["a"]}`, false},
		{"null", `{"summary":"s","user_stories":null,"acceptance_criteria":["a"],"open_questions":[]}`, false},
		{"dup", `{"summary":"s","summary":"t","user_stories":["u"],"acceptance_criteria":["a"],"open_questions":[]}`, false},
		{"wrong type", `{"summary":1,"user_stories":["u"],"acceptance_criteria":["a"],"open_questions":[]}`, false},
		{"trailing", `{"summary":"s","user_stories":["u"],"acceptance_criteria":["a"],"open_questions":[]} {}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var v POSpec
			err := DecodeStrict([]byte(c.in), &v)
			if c.ok != (err == nil) {
				t.Fatalf("ok=%v err=%v", c.ok, err)
			}
			if err != nil && !errors.Is(err, ErrInvalidDeliverable) {
				t.Fatalf("erreur non typée: %v", err)
			}
		})
	}
}

func TestParseTechDesign(t *testing.T) {
	ticket := `{"title":"T","body":"B","acceptance_criteria":["c"]}`
	mk := func(tickets string) string {
		return "```json\n{\"summary\":\"s\",\"decisions\":[{\"decision\":\"d\",\"rationale\":\"r\",\"rejected_alternatives\":[]}],\"risks\":[],\"tickets\":[" + tickets + "]}\n```"
	}
	if _, err := ParseTechDesign(mk(ticket)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseTechDesign(mk("")); err == nil {
		t.Fatal("0 ticket accepté")
	}
	five := strings.TrimSuffix(strings.Repeat(ticket+",", MaxTickets), ",")
	if _, err := ParseTechDesign(mk(five)); err != nil {
		t.Fatalf("%d tickets refusés : %v", MaxTickets, err)
	}
	six := strings.TrimSuffix(strings.Repeat(ticket+",", MaxTickets+1), ",")
	if _, err := ParseTechDesign(mk(six)); err == nil {
		t.Fatalf("%d tickets acceptés", MaxTickets+1)
	}
	if _, err := ParseTechDesign(mk(`{"title":"T","body":"B","acceptance_criteria":["c"],"extra":1}`)); err == nil {
		t.Fatal("clé inconnue imbriquée acceptée")
	}
	if _, err := ParseTechDesign(mk(`{"title":"T","body":"B","acceptance_criteria":[" "]}`)); err == nil {
		t.Fatal("critère vide accepté")
	}
	if _, err := ParseTechDesign(mk(`{"title":"T","body":"B","acceptance_criteria":["c"," c "]}`)); err == nil {
		t.Fatal("critère dupliqué après normalisation accepté")
	}
}

func TestParseReview(t *testing.T) {
	mk := func(verdict, sha, findings string) string {
		return `{"verdict":"` + verdict + `","sha":"` + sha + `","findings":[` + findings + `],"summary":"ok"}`
	}
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"approved", mk("approved", sha1, ""), true},
		{"approved with P2", mk("approved", sha1, `{"severity":"P2","file":"a.go","message":"m"}`), true},
		{"approved with P1", mk("approved", sha1, `{"severity":"P1","file":"a.go","message":"m"}`), false},
		{"approved with P0", mk("approved", sha1, `{"severity":"P0","file":"a.go","message":"m"}`), false},
		{"changes", mk("changes_requested", sha1, `{"severity":"P1","file":"a.go","message":"m"}`), true},
		{"changes without findings", mk("changes_requested", sha1, ""), false},
		{"bad verdict", mk("lgtm", sha1, ""), false},
		{"bad severity", mk("changes_requested", sha1, `{"severity":"P9","file":"a","message":"m"}`), false},
		{"wrong sha", mk("approved", sha2, ""), false},
		{"unknown key", `{"verdict":"approved","sha":"` + sha1 + `","findings":[],"summary":"s","score":3}`, false},
		{"not json", "pas de livrable", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseReview("prose\n"+c.in, sha1)
			if c.ok != (err == nil) {
				t.Fatalf("ok=%v err=%v", c.ok, err)
			}
		})
	}
}

func TestParseQA(t *testing.T) {
	mk := func(verdict, checks, acc string) string {
		return "```json\n" + `{"verdict":"` + verdict + `","sha":"` + sha1 + `","checks":[` + checks + `],"acceptance":[` + acc + `],"summary":"s"}` + "\n```"
	}
	okCheck := `{"command":"go test ./...","exit_code":0}`
	badCheck := `{"command":"go test ./...","exit_code":1}`
	met := `{"criterion":"c","status":"met","evidence":"test X"}`
	notMet := `{"criterion":"c","status":"not_met","evidence":""}`
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"passed", mk("passed", okCheck, met), true},
		{"passed failing check", mk("passed", badCheck, met), false},
		{"passed not met", mk("passed", okCheck, notMet), false},
		{"passed no checks", mk("passed", "", met), false},
		{"passed met without evidence", mk("passed", okCheck, `{"criterion":"c","status":"met","evidence":""}`), false},
		{"failed", mk("failed", badCheck, notMet), true},
		{"bad status", mk("failed", okCheck, `{"criterion":"c","status":"maybe","evidence":""}`), false},
		{"bad verdict", mk("ok", okCheck, met), false},
		{"exit code string", mk("passed", `{"command":"x","exit_code":"0"}`, met), false},
		{"criterion missing", mk("failed", badCheck, ""), false},
		{"criterion invented", mk("failed", badCheck, `{"criterion":"x","status":"not_met","evidence":""}`), false},
		{"criterion whitespace changed", mk("passed", okCheck, `{"criterion":" c ","status":"met","evidence":"test"}`), false},
		{"criterion duplicated", mk("passed", okCheck, met+","+met), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseQA(c.in, sha1, []string{"c"})
			if c.ok != (err == nil) {
				t.Fatalf("ok=%v err=%v", c.ok, err)
			}
		})
	}
	if _, err := ParseQA(mk("passed", okCheck, met), "abc", []string{"c"}); err == nil {
		t.Fatal("sha attendu invalide accepté")
	}
	two := met + `,{"criterion":"d","status":"met","evidence":"test Y"}`
	if _, err := ParseQA(mk("passed", okCheck, two), sha1, []string{"c", "d"}); err != nil {
		t.Fatalf("couverture exacte de deux critères refusée: %v", err)
	}
}

func TestParsePOAndDevelop(t *testing.T) {
	if _, err := ParsePOSpec(`{"summary":"s","user_stories":["u"],"acceptance_criteria":["a"],"open_questions":["q?"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePOSpec(`{"summary":"","user_stories":["u"],"acceptance_criteria":["a"],"open_questions":[]}`); err == nil {
		t.Fatal("summary vide accepté")
	}
	if _, err := ParseDevelop(`{"summary":"s","files_changed":["a.go"],"tests":[{"command":"go test","exit_code":0}],"notes":""}`); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDevelop(`{"summary":"s","files_changed":[],"tests":[{"command":"go test","exit_code":1.5}],"notes":""}`); err == nil {
		t.Fatal("exit_code non entier accepté")
	}
}

func TestRunOutcomeAcceptable(t *testing.T) {
	cases := []struct {
		status               string
		partial, interrupted bool
		ok                   bool
	}{
		{RunCompleted, false, false, true},
		{RunCompleted, true, false, false},
		{RunCompleted, false, true, false},
		{RunFailed, false, false, false},
		{RunLost, false, false, false},
	}
	for _, c := range cases {
		if err := RunOutcomeAcceptable(c.status, c.partial, c.interrupted); c.ok != (err == nil) {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestDevelopNotesIsOptionalButStillTyped(t *testing.T) {
	v, err := ParseDevelop(`{"summary":"s","files_changed":["a.go"],"tests":[{"command":"go test","exit_code":0}]}`)
	if err != nil || v.Notes != "" {
		t.Fatalf("notes absent doit être accepté : %v", err)
	}
	for _, bad := range []string{
		`{"summary":"s","files_changed":[],"tests":[],"notes":null}`,
		`{"summary":"s","files_changed":[],"tests":[],"notes":3}`,
		`{"summary":"s","files_changed":[],"tests":[],"remarques":"x"}`,
		`{"summary":"s","tests":[]}`,
	} {
		if _, err := ParseDevelop(bad); err == nil {
			t.Fatalf("doit être refusé : %s", bad)
		}
	}
}
