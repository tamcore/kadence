package store

import (
	"context"
	"errors"
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
	err := r.pool.QueryRow(ctx,
		`INSERT INTO documents (owner_user_id, scope, filename, mime, source_type, extracted_markdown)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
		d.OwnerUserID, d.Scope, d.Filename, d.Mime, d.SourceType, d.ExtractedMarkdown).
		Scan(&d.ID, &d.CreatedAt)
	if err != nil {
		return model.Document{}, fmt.Errorf("insert document: %w", err)
	}
	return d, nil
}

// GetByID returns a document including its extracted markdown, or ErrNotFound.
func (r *DocumentRepository) GetByID(ctx context.Context, id int64) (model.Document, error) {
	var d model.Document
	err := r.pool.QueryRow(ctx,
		`SELECT id, owner_user_id, scope, filename, mime, source_type, extracted_markdown, created_at
		 FROM documents WHERE id = $1`, id).
		Scan(&d.ID, &d.OwnerUserID, &d.Scope, &d.Filename, &d.Mime, &d.SourceType, &d.ExtractedMarkdown, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Document{}, ErrNotFound
	}
	if err != nil {
		return model.Document{}, fmt.Errorf("get document: %w", err)
	}
	return d, nil
}

// ListByOwner returns a user's documents, newest first. The (potentially
// large) extracted_markdown column is omitted and left empty.
func (r *DocumentRepository) ListByOwner(ctx context.Context, ownerUserID int64) ([]model.Document, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, owner_user_id, scope, filename, mime, source_type, created_at
		 FROM documents WHERE owner_user_id = $1 ORDER BY created_at DESC`, ownerUserID)
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

// Delete removes a document owned by ownerUserID (cascades to chunks).
func (r *DocumentRepository) Delete(ctx context.Context, id, ownerUserID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1 AND owner_user_id = $2`, id, ownerUserID)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	return nil
}

// DeletePublic removes a public document (cascades to chunks).
func (r *DocumentRepository) DeletePublic(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1 AND scope = 'public'`, id)
	if err != nil {
		return fmt.Errorf("delete public document: %w", err)
	}
	return nil
}
