package postgres

import (
	"path/filepath"

	"harukizmoe/pimoe/internal/session"
)

func sessionRecordToMeta(transcriptRoot string, record SessionRecord) session.SessionMeta {
	return session.SessionMeta{
		ID:        record.ID,
		OwnerID:   record.OwnerID,
		Path:      filepath.Join(transcriptRoot, record.ID+".jsonl"),
		Title:     record.Title,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
		Config: session.SessionConfig{
			ProviderName:  record.ProviderName,
			SessionPrompt: record.SessionPrompt,
			MaxSteps:      record.MaxSteps,
		},
	}
}

func sessionMetaToRecord(meta session.SessionMeta) SessionRecord {
	return SessionRecord{
		ID:            meta.ID,
		OwnerID:       meta.OwnerID,
		Title:         meta.Title,
		ProviderName:  meta.Config.ProviderName,
		SessionPrompt: meta.Config.SessionPrompt,
		MaxSteps:      meta.Config.MaxSteps,
		CreatedAt:     meta.CreatedAt,
		UpdatedAt:     meta.UpdatedAt,
	}
}
