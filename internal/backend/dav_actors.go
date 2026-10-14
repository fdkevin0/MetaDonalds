package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
)

func davActorDirs(media javdb.Media, modTime time.Time) []davNode {
	out := []davNode{}
	seen := map[string]bool{}
	for _, actor := range media.Actors {
		if !davID.MatchString(actor.ID) || seen[actor.ID] {
			continue
		}
		seen[actor.ID] = true
		out = append(out, davNode{name: davReadableName(actor.Name, actor.ID), kind: "actor", dir: true, actor: actor, modTime: modTime})
	}
	return out
}

func (d *davServer) actorNode(ctx context.Context, catalog davCatalog, actor davNode, parts []string) (davNode, error) {
	if len(parts) == 0 {
		return actor, nil
	}
	if len(parts) != 1 {
		return davNode{}, os.ErrNotExist
	}
	switch parts[0] {
	case "metadata.json":
		profile, err := catalog.Actor(ctx, actor.actor.ID)
		if err != nil {
			return davNode{}, err
		}
		var data bytes.Buffer
		if err := json.Indent(&data, profile, "", "  "); err != nil {
			return davNode{}, err
		}
		data.WriteByte('\n')
		return davNode{name: "metadata.json", kind: "bytes", mime: "application/json", data: data.Bytes(), size: int64(data.Len()), modTime: actor.modTime}, nil
	case "avatar.jpg":
		if actor.actor.AvatarURL != "" {
			node, err := d.imageNode(ctx, "avatar.jpg", actor.actor.AvatarURL)
			node.modTime = actor.modTime
			return node, err
		}
	}
	return davNode{}, os.ErrNotExist
}
