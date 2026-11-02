package store

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"

	"household/internal/auth"
	"household/internal/value"
)

type DocumentExportFile struct {
	Key    string
	Path   string
	SHA256 string
	Size   int64
}
type DocumentArchive struct {
	Manifest Record
	Files    []DocumentExportFile
}

func (s *Store) DocumentExport(ctx context.Context, id auth.Identity, ids []string) (DocumentArchive, error) {
	out := DocumentArchive{Files: []DocumentExportFile{}}
	if len(ids) < 1 || len(ids) > 50 {
		return out, Invalid{"1 bis 50 Dokumente für das Archiv auswählen"}
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return out, Invalid{"Doppelte Dokumentauswahl"}
	}
	for _, rid := range ids {
		if !value.ValidUUID(rid) {
			return out, Invalid{"Dokumentauswahl prüfen"}
		}
	}
	q, err := s.documentRead(ctx, id)
	if err != nil {
		return out, err
	}
	defer q.close()
	documents := []Record{}
	total := int64(0)
	for _, rid := range ids {
		d, err := documentRecord(q.ctx, q.tx, id.HouseholdID, rid)
		if err != nil {
			return out, err
		}
		d["links"], err = s.documentCoreLinks(q.ctx, q.tx, id, rid)
		if err != nil {
			return out, err
		}
		d["deadlines"], err = energyRows(q.ctx, q.tx, 100, `SELECT to_jsonb(x)-'confirmed_by'-'household_id' FROM documents.deadlines x WHERE household_id=$1 AND document_id=$2 ORDER BY due_on,id LIMIT 101`, id.HouseholdID, rid)
		if err != nil {
			return out, err
		}
		d["scan_sets"], err = energyRows(q.ctx, q.tx, 100, `SELECT jsonb_build_object('id',s.id,'label',s.label,'pages',(SELECT jsonb_agg(jsonb_build_object('version_id',p.version_id,'page_number',p.page_number,'position',p.position) ORDER BY p.position) FROM documents.scan_pages p WHERE p.set_id=s.id)) FROM documents.scan_sets s WHERE s.household_id=$1 AND s.document_id=$2 ORDER BY s.id LIMIT 101`, id.HouseholdID, rid)
		if err != nil {
			return out, err
		}
		versions, err := energyRows(q.ctx, q.tx, 100, `SELECT jsonb_build_object('id',v.id,'ordinal',v.ordinal,'source',v.source,'page_count',v.page_count,'extracted_text',v.extracted_text,'text_provider',v.text_provider,'sha256',encode(v.original_sha256,'hex'),'size_bytes',v.original_size,'original_name',a.original_name,'media_type',a.media_type,'storage_key',a.storage_key) FROM documents.file_versions v JOIN core.attachments a ON a.id=v.attachment_id AND a.state='ready' AND a.module_generation=$3 WHERE v.household_id=$1 AND v.document_id=$2 ORDER BY v.ordinal LIMIT 101`, id.HouseholdID, rid, q.generation)
		if err != nil {
			return out, err
		}
		for _, v := range versions {
			if len(out.Files) >= 100 {
				return out, Invalid{"Archiv auf höchstens 100 Dateien begrenzen"}
			}
			size := Int(v["size_bytes"])
			total += size
			if size < 1 || size > 10<<20 || total > 64<<20 {
				return out, Invalid{"Archiv auf 64 MiB Originaldateien begrenzen"}
			}
			key := String(v["storage_key"])
			digest := String(v["sha256"])
			if !value.ValidUUID(key) {
				return out, ErrNotFound
			}
			if raw, e := hex.DecodeString(digest); e != nil || len(raw) != 32 {
				return out, ErrNotFound
			}
			ext := ".pdf"
			switch String(v["media_type"]) {
			case "image/jpeg":
				ext = ".jpg"
			case "image/png":
				ext = ".png"
			}
			path := "originals/" + rid + "/" + String(v["id"]) + ext
			out.Files = append(out.Files, DocumentExportFile{Key: key, Path: path, SHA256: digest, Size: size})
			delete(v, "storage_key")
			v["archive_path"] = path
		}
		d["versions"] = versions
		delete(d, "household_id")
		delete(d, "created_by")
		documents = append(documents, d)
	}
	out.Manifest = Record{"format": "household.documents.v1", "generation": q.generation, "timezone": q.timezone, "documents": documents, "ownership": "Independent local copies. Source systems are never modified.", "retention": "Archiving preserves originals. Owner purge requires a verified backup; old backups keep their own retention.", "selection": strings.Join(ids, ",")}
	return out, nil
}
