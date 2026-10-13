package pikpak

import (
	"context"
	"github.com/fdkevin0/MetaDonalds/internal/core"
	"net/http"
)

type Downloader struct {
	Remote string
	Client *http.Client
}

func (d *Downloader) Submit(ctx context.Context, r core.Resource) core.Submission {
	r, err := core.NormalizeResource(r)
	if err != nil {
		return core.Submission{Status: "rejected", Message: err.Error()}
	}
	if err := SubmitHash(ctx, d.Client, d.Remote, r.InfoHash); err != nil {
		// A transport error or upstream failure may follow acceptance. Never retry here.
		return core.Submission{Status: "unknown", InfoHash: r.InfoHash, Message: "Submission not confirmed; check the downloader before retrying."}
	}
	return core.Submission{Status: "accepted", InfoHash: r.InfoHash}
}
