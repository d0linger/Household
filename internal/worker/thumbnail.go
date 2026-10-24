package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"

	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (w *Worker) thumbnail(ctx context.Context, job, lease string) error {
	var aid, key, hid, mid string
	var generation int64
	err := w.Pool.QueryRow(ctx, "SELECT payload->>'attachment_id',payload->>'storage_key',household_id,module_id,module_generation FROM core.jobs WHERE id=$1 AND lease_token=$2", job, lease).Scan(&aid, &key, &hid, &mid, &generation)
	if err != nil {
		return err
	}
	if !value.ValidUUID(aid) || !value.ValidUUID(key) {
		return errors.New("invalid thumbnail job")
	}
	root, err := os.OpenRoot(w.Cfg.UploadDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(key)
	if err != nil {
		return err
	}
	cfg, _, err := image.DecodeConfig(file)
	if err != nil || int64(cfg.Width)*int64(cfg.Height) > 20000000 {
		_ = file.Close()
		return errors.New("invalid source image")
	}
	if _, err = file.Seek(0, 0); err != nil {
		_ = file.Close()
		return err
	}
	source, _, err := image.Decode(file)
	_ = file.Close()
	if err != nil {
		return err
	}
	width, height := cfg.Width, cfg.Height
	if width > 512 || height > 512 {
		if width >= height {
			height = max(1, height*512/width)
			width = 512
		} else {
			width = max(1, width*512/height)
			height = 512
		}
	}
	preview := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		for x := 0; x < width; x++ {
			preview.Set(x, y, source.At(x*cfg.Width/width, y*cfg.Height/height))
		}
	}
	var buffer bytes.Buffer
	if err = png.Encode(&buffer, preview); err != nil {
		return err
	}
	newKey := value.UUID()
	out, err := root.OpenFile(newKey, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	retained := false
	defer func() {
		if !retained {
			_ = root.Remove(newKey)
		}
	}()
	_, err = out.Write(buffer.Bytes())
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", hid); err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, "SELECT true FROM core.jobs j JOIN core.module_states m ON m.household_id=j.household_id AND m.module_id=j.module_id JOIN core.attachments a ON a.id=$3 AND a.household_id=j.household_id WHERE j.id=$1 AND j.lease_token=$2 AND j.lease_until>now() AND a.state='ready' AND m.state='enabled' AND m.data_generation=$4 FOR UPDATE OF j FOR SHARE OF m,a", job, lease, aid, generation).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	hash := sha256.Sum256(buffer.Bytes())
	tag, err := tx.Exec(ctx, "INSERT INTO core.attachment_derivatives(attachment_id,variant,storage_key,media_type,width,height,size_bytes,sha256) VALUES($1,'preview',$2,'image/png',$3,$4,$5,$6) ON CONFLICT(attachment_id,variant) DO NOTHING", aid, newKey, width, height, buffer.Len(), hash[:])
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.jobs SET state='done',finished_at=now(),lease_until=NULL WHERE id=$1 AND lease_token=$2", job, lease); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	retained = tag.RowsAffected() == 1
	return nil
}
