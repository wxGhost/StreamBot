package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"streamer-bot/internal/models"
)

type DB struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 5
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &DB{pool: pool}, nil
}

func (d *DB) Close() {
	d.pool.Close()
}

// CreateProposal inserts a new proposal and returns it with its assigned ID.
func (d *DB) CreateProposal(ctx context.Context, p *models.Proposal) error {
	const q = `
		INSERT INTO proposals (user_id, username, first_name, type, content)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`
	return d.pool.QueryRow(ctx, q,
		p.UserID, p.Username, p.FirstName, p.Type, p.Content,
	).Scan(&p.ID, &p.CreatedAt)
}

// SetMessageID stores the streamer-chat message ID on the proposal.
func (d *DB) SetMessageID(ctx context.Context, proposalID int, msgID int64) error {
	const q = `UPDATE proposals SET message_id = $1 WHERE id = $2`
	_, err := d.pool.Exec(ctx, q, msgID, proposalID)
	return err
}

// GetProposal fetches a single proposal with vote totals.
func (d *DB) GetProposal(ctx context.Context, id int) (*models.Proposal, error) {
	const q = `
		SELECT id, user_id, username, first_name, type, content, message_id,
		       status, likes, dislikes, created_at
		FROM proposals_with_votes WHERE id = $1`
	p := &models.Proposal{}
	err := d.pool.QueryRow(ctx, q, id).Scan(
		&p.ID, &p.UserID, &p.Username, &p.FirstName,
		&p.Type, &p.Content, &p.MessageID,
		&p.Status, &p.Likes, &p.Dislikes, &p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ListProposals returns proposals filtered by status, newest first.
func (d *DB) ListProposals(ctx context.Context, status models.ProposalStatus, limit, offset int) ([]*models.Proposal, error) {
	const q = `
		SELECT id, user_id, username, first_name, type, content, message_id,
		       status, likes, dislikes, created_at
		FROM proposals_with_votes
		WHERE status = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	rows, err := d.pool.Query(ctx, q, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProposals(rows)
}

// ListTop returns top proposals ordered by net score (likes - dislikes).
func (d *DB) ListTop(ctx context.Context, limit int) ([]*models.Proposal, error) {
	const q = `
		SELECT id, user_id, username, first_name, type, content, message_id,
		       status, likes, dislikes, created_at
		FROM proposals_with_votes
		WHERE status = 'top'
		ORDER BY (likes - dislikes) DESC, created_at DESC
		LIMIT $1`
	rows, err := d.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProposals(rows)
}

// SetStatus updates the status of a proposal.
func (d *DB) SetStatus(ctx context.Context, id int, status models.ProposalStatus) error {
	const q = `UPDATE proposals SET status = $1 WHERE id = $2`
	_, err := d.pool.Exec(ctx, q, status, id)
	return err
}

// DeleteProposal removes a proposal and its votes from the database.
func (d *DB) DeleteProposal(ctx context.Context, id int) error {
	const q = `DELETE FROM proposals WHERE id = $1`
	_, err := d.pool.Exec(ctx, q, id)
	return err
}

// CountProposals returns the total number of proposals regardless of status.
func (d *DB) CountProposals(ctx context.Context) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx, `SELECT COUNT(*) FROM proposals`).Scan(&n)
	return n, err
}

// Upsert a vote; returns updated likes/dislikes counts for the proposal.
func (d *DB) UpsertVote(ctx context.Context, proposalID int, userID int64, value int) (likes, dislikes int, err error) {
	const upsert = `
		INSERT INTO votes (proposal_id, user_id, value)
		VALUES ($1, $2, $3)
		ON CONFLICT (proposal_id, user_id) DO UPDATE SET value = EXCLUDED.value`
	_, err = d.pool.Exec(ctx, upsert, proposalID, userID, value)
	if err != nil {
		return 0, 0, err
	}
	const counts = `
		SELECT
			COALESCE(SUM(CASE WHEN value = 1  THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN value = -1 THEN 1 ELSE 0 END), 0)
		FROM votes WHERE proposal_id = $1`
	err = d.pool.QueryRow(ctx, counts, proposalID).Scan(&likes, &dislikes)
	return likes, dislikes, err
}

// GetVoteByUser returns the user's vote value (1 or -1) or 0 if not voted.
func (d *DB) GetVoteByUser(ctx context.Context, proposalID int, userID int64) (int, error) {
	var v int
	err := d.pool.QueryRow(ctx,
		`SELECT value FROM votes WHERE proposal_id = $1 AND user_id = $2`,
		proposalID, userID,
	).Scan(&v)
	if err != nil {
		// pgx returns pgx.ErrNoRows — treat as no vote
		return 0, nil
	}
	return v, nil
}

// --- helpers ---

type rowScanner interface {
	Scan(dest ...any) error
	Next() bool
	Err() error
}

func scanProposals(rows rowScanner) ([]*models.Proposal, error) {
	var out []*models.Proposal
	for rows.Next() {
		p := &models.Proposal{}
		if err := rows.Scan(
			&p.ID, &p.UserID, &p.Username, &p.FirstName,
			&p.Type, &p.Content, &p.MessageID,
			&p.Status, &p.Likes, &p.Dislikes, &p.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountByStatus returns the count of proposals with a given status.
func (d *DB) CountByStatus(ctx context.Context, status models.ProposalStatus) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM proposals WHERE status = $1`, status,
	).Scan(&n)
	return n, err
}
