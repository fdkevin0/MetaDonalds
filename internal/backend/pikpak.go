package backend

import (
	"context"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/pikpak"
	"net/http"
)

var NewRCClient = pikpak.NewRCClient

func submitViaRC(ctx context.Context, c *http.Client, remote, hash string) error {
	return pikpak.SubmitHash(ctx, c, remote, hash)
}
