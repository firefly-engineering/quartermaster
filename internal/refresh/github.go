package refresh

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// query asks GitHub about one ref, an issue or a PR alike: the note links both as
// `/issues/<n>`, so the number alone does not say which. For an issue it asks what closed it,
// a commit or a PR, which `gh issue view --json` has no field for.
const query = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issueOrPullRequest(number: $number) {
      __typename
      ... on Issue {
        state
        stateReason
        url
        timelineItems(itemTypes: [CLOSED_EVENT], last: 1) {
          nodes { ... on ClosedEvent { closer { __typename ... on Commit { url } ... on PullRequest { url } } } }
        }
      }
      ... on PullRequest { state url }
    }
  }
}`

// ghReply is gh's answer to query.
type ghReply struct {
	Data struct {
		Repository *struct {
			IssueOrPullRequest *struct {
				Typename      string `json:"__typename"`
				State         string `json:"state"`
				StateReason   string `json:"stateReason"`
				URL           string `json:"url"`
				TimelineItems struct {
					Nodes []struct {
						Closer *struct {
							URL string `json:"url"`
						} `json:"closer"`
					} `json:"nodes"`
				} `json:"timelineItems"`
			} `json:"issueOrPullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// evidence is what GitHub proves about a ref: a state to move to with its summary, or none and
// why.
type evidence struct {
	to      note.State // 0: no move
	summary string
	reason  string
}

// github asks GitHub about ref once per refresh, and reads the answer as evidence (spec §11):
// a merged PR is DONE; an issue closed as completed is DONE, with what closed it; one closed as
// not planned or as a duplicate is DROPPED. Anything else, an open ref, a PR closed unmerged,
// an issue closed with no reason, proves nothing.
func (g *gatherer) github(ctx context.Context, ref note.Ref) evidence {
	if e, ok := g.refs[ref]; ok {
		return e
	}
	e := g.ask(ctx, ref)
	g.refs[ref] = e
	return e
}

func (g *gatherer) ask(ctx context.Context, ref note.Ref) evidence {
	if g.GH == nil {
		return evidence{reason: "gh: not available"}
	}
	owner, name, _ := strings.Cut(ref.Repo, "/")
	out, err := g.GH(ctx, "api", "graphql", "-f", "query="+query, "-f", "owner="+owner, "-f", "name="+name,
		"-F", "number="+strconv.Itoa(ref.N))
	if err != nil {
		return evidence{reason: "gh: " + err.Error()}
	}
	var r ghReply
	if err := json.Unmarshal(out, &r); err != nil {
		return evidence{reason: fmt.Sprintf("gh: %s: %v", ref, err)}
	}
	if r.Data.Repository == nil || r.Data.Repository.IssueOrPullRequest == nil {
		return evidence{reason: ref.String() + " is not on GitHub"}
	}
	x := r.Data.Repository.IssueOrPullRequest
	switch {
	case x.State == "OPEN":
		return evidence{reason: ref.String() + " is open"}
	case x.Typename == "PullRequest" && x.State == "MERGED":
		return evidence{to: note.Done, summary: "merged " + x.URL}
	case x.Typename == "PullRequest":
		return evidence{reason: ref.String() + " was closed without being merged"}
	case x.Typename != "Issue" || x.State != "CLOSED":
		return evidence{reason: fmt.Sprintf("%s is %s %s", ref, x.Typename, x.State)}
	}
	switch x.StateReason {
	case "COMPLETED":
		if n := x.TimelineItems.Nodes; len(n) > 0 && n[len(n)-1].Closer != nil && n[len(n)-1].Closer.URL != "" {
			return evidence{to: note.Done, summary: "closed by " + n[len(n)-1].Closer.URL}
		}
		return evidence{to: note.Done, summary: "closed as completed: " + x.URL}
	case "NOT_PLANNED":
		return evidence{to: note.Dropped, summary: "closed as not planned: " + x.URL}
	case "DUPLICATE":
		return evidence{to: note.Dropped, summary: "closed as a duplicate: " + x.URL}
	case "":
		return evidence{reason: ref.String() + " is closed with no reason recorded"}
	}
	return evidence{reason: fmt.Sprintf("%s is closed as %s", ref, x.StateReason)}
}
