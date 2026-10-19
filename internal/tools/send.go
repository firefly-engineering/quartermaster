package tools

import (
	"context"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/send"
)

// PromptSendInput is prompt_send's input.
type PromptSendInput struct {
	Date string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	Item string `json:"item" jsonschema:"the id of the work item whose herdr-prompt block to send"`
}

// PromptSendOutput is prompt_send's result.
type PromptSendOutput struct {
	Written
	Item string `json:"item" jsonschema:"the work item sent"`
	To   string `json:"to" jsonschema:"the herdr workspace label prompted"`
	Pane string `json:"pane" jsonschema:"the herdr pane prompted"`
}

// promptSend sends item's prompt block to its herdr session, as `worklog send` does
// (spec §9.1), through package send and its validation. It records the prompted pane on the
// item and leaves its state: the skill records the send.
func (d Deps) promptSend(ctx context.Context, in PromptSendInput) (PromptSendOutput, error) {
	vault, err := d.vault()
	if err != nil {
		return PromptSendOutput{}, err
	}
	date := in.Date
	if date == "" {
		date = d.today().Format(time.DateOnly)
	}
	sent, err := send.Sender{Config: d.Config, Engine: d.engine(), Herdr: d.Herdr, Env: d.Env}.Send(ctx, vault, date, in.Item)
	if err != nil {
		return PromptSendOutput{}, err
	}
	n := sent.Note
	return PromptSendOutput{
		Written: Written{Path: sent.Path, Changed: n.Changed, Committed: n.Committed, Skipped: n.Skipped},
		Item:    sent.ID, To: sent.To, Pane: sent.Pane,
	}, nil
}
