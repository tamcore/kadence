package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// DocumentRepository accesses the documents table.
type DocumentRepository struct{ pool *pgxpool.Pool }

// NewDocumentRepository returns a DocumentRepository.
func NewDocumentRepository(pool *pgxpool.Pool) *DocumentRepository {
	return &DocumentRepository{pool: pool}
}

// Create inserts a new document.
func (r *DocumentRepository) Create(ctx context.Context, d model.Document) (model.Document, error) {
	status := d.ExtractionStatus
	if status == "" {
		status = model.ExtractionStatusNotNeeded
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO documents (owner_user_id, scope, filename, mime, source_type,
		                        extracted_markdown, extraction_status, raw_bytes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, created_at, extraction_status`,
		d.OwnerUserID, d.Scope, d.Filename, d.Mime, d.SourceType, d.ExtractedMarkdown,
		status, d.RawBytes).
		Scan(&d.ID, &d.CreatedAt, &d.ExtractionStatus)
	if err != nil {
		return model.Document{}, fmt.Errorf("insert document: %w", err)
	}
	return d, nil
}

// ListVisibleByIDs returns every requested document in request order,
// including extracted markdown. A document is visible when it is public or
// private and owned by userID. Missing, deleted, and invisible documents all
// fail closed with ErrNotFound and no partial result.
func (r *DocumentRepository) ListVisibleByIDs(
	ctx context.Context, userID int64, ids []int64,
) ([]model.Document, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT d.id, d.owner_user_id, d.scope, d.filename, d.mime,
		        d.source_type, d.extracted_markdown, d.created_at
		   FROM unnest($1::bigint[]) WITH ORDINALITY AS requested(id, ordinal)
		   JOIN documents d
		     ON d.id = requested.id
		    AND (d.scope = $2 OR (d.scope = $3 AND d.owner_user_id = $4))
		  ORDER BY requested.ordinal`,
		ids, model.ScopePublic, model.ScopePrivate, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list visible documents: %w", err)
	}
	defer rows.Close()

	documents := make([]model.Document, 0, len(ids))
	for rows.Next() {
		var document model.Document
		if err := rows.Scan(
			&document.ID, &document.OwnerUserID, &document.Scope, &document.Filename,
			&document.Mime, &document.SourceType, &document.ExtractedMarkdown,
			&document.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan visible document: %w", err)
		}
		documents = append(documents, document)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list visible documents: %w", err)
	}
	if len(documents) != len(ids) {
		return nil, ErrNotFound
	}
	return documents, nil
}

// ListByOwner returns a user's documents, newest first. The (potentially
// large) extracted_markdown column is omitted and left empty.
func (r *DocumentRepository) ListByOwner(ctx context.Context, ownerUserID int64) ([]model.Document, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, owner_user_id, scope, filename, mime, source_type, created_at
		 FROM documents
		 WHERE owner_user_id = $1 AND scope = $2
		 ORDER BY created_at DESC`,
		ownerUserID, model.ScopePrivate,
	)
	if err != nil {
		return nil, fmt.Errorf("list documents by owner: %w", err)
	}
	defer rows.Close()
	return scanDocumentRows(rows)
}

// ListPublic returns all public documents, newest first. The (potentially
// large) extracted_markdown column is omitted and left empty.
func (r *DocumentRepository) ListPublic(ctx context.Context) ([]model.Document, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, owner_user_id, scope, filename, mime, source_type, created_at
		 FROM documents WHERE scope = 'public' ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list public documents: %w", err)
	}
	defer rows.Close()
	return scanDocumentRows(rows)
}

// scanDocumentRows scans rows selected without extracted_markdown, leaving
// that field as the empty string.
func scanDocumentRows(rows pgx.Rows) ([]model.Document, error) {
	var out []model.Document
	for rows.Next() {
		var d model.Document
		if err := rows.Scan(&d.ID, &d.OwnerUserID, &d.Scope, &d.Filename, &d.Mime, &d.SourceType, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan document: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Delete removes a document owned by ownerUserID (cascades to chunks), or
// ErrNotFound if no row matched (wrong id or not the owner).
func (r *DocumentRepository) Delete(ctx context.Context, id, ownerUserID int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1 AND owner_user_id = $2`, id, ownerUserID)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePublic removes a public document (cascades to chunks), or
// ErrNotFound if no row matched.
func (r *DocumentRepository) DeletePublic(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1 AND scope = 'public'`, id)
	if err != nil {
		return fmt.Errorf("delete public document: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimPendingExtraction atomically moves up to limit pending documents to
// running and returns them with their raw upload bytes. FOR UPDATE SKIP LOCKED
// means concurrent workers never claim the same row.
func (r *DocumentRepository) ClaimPendingExtraction(
	ctx context.Context, limit int,
) ([]model.Document, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`UPDATE documents SET extraction_status = $1
		  WHERE id IN (
		        SELECT id FROM documents
		         WHERE extraction_status = $2
		         ORDER BY id
		           FOR UPDATE SKIP LOCKED
		         LIMIT $3
		  )
		 RETURNING id, owner_user_id, scope, filename, mime, source_type,
		           extracted_markdown, extraction_status, raw_bytes, created_at`,
		model.ExtractionStatusRunning, model.ExtractionStatusPending, limit)
	if err != nil {
		return nil, fmt.Errorf("claim pending extraction: %w", err)
	}
	defer rows.Close()

	var out []model.Document
	for rows.Next() {
		var d model.Document
		if err := rows.Scan(
			&d.ID, &d.OwnerUserID, &d.Scope, &d.Filename, &d.Mime, &d.SourceType,
			&d.ExtractedMarkdown, &d.ExtractionStatus, &d.RawBytes, &d.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan claimed document: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed documents: %w", err)
	}
	return out, nil
}

// FinishExtraction records the outcome of the page-image pass.
//
// On success the stored upload bytes are released, so a converted PDF is not
// kept twice. On failure they are retained: failures are often transient (a
// provider outage, a rejected request), and discarding the bytes would make the
// document impossible to retry without a re-upload.
func (r *DocumentRepository) FinishExtraction(
	ctx context.Context, id int64, markdown, status string,
) error {
	const query = `
UPDATE documents
   SET extracted_markdown = $1,
       extraction_status = $2,
       raw_bytes = CASE WHEN $2 = $4 THEN raw_bytes ELSE NULL END
 WHERE id = $3`
	if _, err := r.pool.Exec(
		ctx, query, markdown, status, id, model.ExtractionStatusFailed,
	); err != nil {
		return fmt.Errorf("finish extraction for document %d: %w", id, err)
	}
	return nil
}

// RetryFailedExtractions moves failed documents that still hold their upload
// bytes back to pending, so a transient provider failure can be retried without
// a re-upload.
func (r *DocumentRepository) RetryFailedExtractions(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE documents SET extraction_status = $1
		  WHERE extraction_status = $2 AND raw_bytes IS NOT NULL`,
		model.ExtractionStatusPending, model.ExtractionStatusFailed)
	if err != nil {
		return 0, fmt.Errorf("retry failed extractions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RequeueRunningExtractions returns documents stranded in running (a crash or
// shutdown between claim and finish) to pending, so the worker retries them
// instead of leaving them stuck out of the queue forever.
func (r *DocumentRepository) RequeueRunningExtractions(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE documents SET extraction_status = $1 WHERE extraction_status = $2`,
		model.ExtractionStatusPending, model.ExtractionStatusRunning)
	if err != nil {
		return 0, fmt.Errorf("requeue running extractions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MarkExtractionPending queues a document for the page-image pass and stores
// the bytes that pass needs. Called only after the document's initial chunks
// exist, so the worker cannot race that first indexing.
func (r *DocumentRepository) MarkExtractionPending(
	ctx context.Context, id int64, rawBytes []byte,
) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE documents SET extraction_status = $1, raw_bytes = $2 WHERE id = $3`,
		model.ExtractionStatusPending, rawBytes, id); err != nil {
		return fmt.Errorf("queue extraction for document %d: %w", id, err)
	}
	return nil
}
