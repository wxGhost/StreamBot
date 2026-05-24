package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"streamer-bot/models"
)

type DB struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 3
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 5 * time.Minute
	cfg.MaxConnIdleTime = 30 * time.Second
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &DB{pool: pool}, nil
}

func (d *DB) Close() { d.pool.Close() }

// ─── Proposals ────────────────────────────────────────────────────────────────

func (d *DB) CreateProposal(ctx context.Context, p *models.Proposal) error {
	const q = `INSERT INTO proposals (user_id, username, first_name, type, content)
		VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`
	return d.pool.QueryRow(ctx, q,
		p.UserID, p.Username, p.FirstName, p.Type, p.Content,
	).Scan(&p.ID, &p.CreatedAt)
}

func (d *DB) SetMessageID(ctx context.Context, proposalID int, msgID int64) error {
	_, err := d.pool.Exec(ctx, `UPDATE proposals SET message_id=$1 WHERE id=$2`, msgID, proposalID)
	return err
}

func (d *DB) GetProposal(ctx context.Context, id int) (*models.Proposal, error) {
	const q = `SELECT id,user_id,username,first_name,type,content,message_id,status,likes,dislikes,created_at
		FROM proposals_with_votes WHERE id=$1`
	p := &models.Proposal{}
	err := d.pool.QueryRow(ctx, q, id).Scan(
		&p.ID, &p.UserID, &p.Username, &p.FirstName,
		&p.Type, &p.Content, &p.MessageID, &p.Status,
		&p.Likes, &p.Dislikes, &p.CreatedAt,
	)
	return p, err
}

func (d *DB) ListProposals(ctx context.Context, status models.ProposalStatus, limit, offset int) ([]*models.Proposal, error) {
	const q = `SELECT id,user_id,username,first_name,type,content,message_id,status,likes,dislikes,created_at
		FROM proposals_with_votes WHERE status=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := d.pool.Query(ctx, q, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProposals(rows)
}

func (d *DB) ListTop(ctx context.Context, limit int) ([]*models.Proposal, error) {
	const q = `SELECT id,user_id,username,first_name,type,content,message_id,status,likes,dislikes,created_at
		FROM proposals_with_votes WHERE status='top'
		ORDER BY (likes-dislikes) DESC, created_at DESC LIMIT $1`
	rows, err := d.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProposals(rows)
}

func (d *DB) SetStatus(ctx context.Context, id int, status models.ProposalStatus) error {
	_, err := d.pool.Exec(ctx, `UPDATE proposals SET status=$1 WHERE id=$2`, status, id)
	return err
}

func (d *DB) DeleteProposal(ctx context.Context, id int) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM proposals WHERE id=$1`, id)
	return err
}

func (d *DB) CountProposals(ctx context.Context) (int, error) {
	var n int
	return n, d.pool.QueryRow(ctx, `SELECT COUNT(*) FROM proposals`).Scan(&n)
}

func (d *DB) CountByStatus(ctx context.Context, status models.ProposalStatus) (int, error) {
	var n int
	return n, d.pool.QueryRow(ctx, `SELECT COUNT(*) FROM proposals WHERE status=$1`, status).Scan(&n)
}

// ─── Votes ────────────────────────────────────────────────────────────────────

func (d *DB) UpsertVote(ctx context.Context, proposalID int, userID int64, value int) (likes, dislikes int, err error) {
	_, err = d.pool.Exec(ctx,
		`INSERT INTO votes(proposal_id,user_id,value) VALUES($1,$2,$3)
		 ON CONFLICT(proposal_id,user_id) DO UPDATE SET value=EXCLUDED.value`,
		proposalID, userID, value)
	if err != nil {
		return 0, 0, err
	}
	err = d.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(CASE WHEN value=1 THEN 1 ELSE 0 END),0),
		        COALESCE(SUM(CASE WHEN value=-1 THEN 1 ELSE 0 END),0)
		 FROM votes WHERE proposal_id=$1`, proposalID).Scan(&likes, &dislikes)
	return
}

// ─── Blocks ───────────────────────────────────────────────────────────────────

func (d *DB) BlockUser(ctx context.Context, userID int64, until time.Time) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO user_blocks(user_id,blocked_until) VALUES($1,$2)
		 ON CONFLICT(user_id) DO UPDATE SET blocked_until=EXCLUDED.blocked_until, created_at=NOW()`,
		userID, until)
	return err
}

// IsBlocked returns (blocked, error). blocked=true means user cannot send proposals.
func (d *DB) IsBlocked(ctx context.Context, userID int64) (bool, error) {
	var until time.Time
	err := d.pool.QueryRow(ctx,
		`SELECT blocked_until FROM user_blocks WHERE user_id=$1`, userID).Scan(&until)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return time.Now().Before(until), nil
}

// ─── Cooldowns ────────────────────────────────────────────────────────────────

func (d *DB) SetCooldown(ctx context.Context, userID int64, kind string, expiresAt time.Time) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO user_cooldowns(user_id,kind,expires_at) VALUES($1,$2,$3)
		 ON CONFLICT(user_id,kind) DO UPDATE SET expires_at=EXCLUDED.expires_at`,
		userID, kind, expiresAt)
	return err
}

// GetCooldown returns expiry time if active, zero time if not.
func (d *DB) GetCooldown(ctx context.Context, userID int64, kind string) (time.Time, error) {
	var expires time.Time
	err := d.pool.QueryRow(ctx,
		`SELECT expires_at FROM user_cooldowns WHERE user_id=$1 AND kind=$2 AND expires_at>NOW()`,
		userID, kind).Scan(&expires)
	if err == pgx.ErrNoRows {
		return time.Time{}, nil
	}
	return expires, err
}

// ─── Game stacking ────────────────────────────────────────────────────────────

type GameProposer struct {
	UserID    *int64  `json:"user_id,omitempty"`
	Username  *string `json:"username,omitempty"`
	FirstName *string `json:"first_name,omitempty"`
}

type GameStack struct {
	ID            int
	GameTitle     string
	GameTitleOrig string
	Count         int
	Proposers     []GameProposer
	LastProposed  time.Time
}

func NormalizeGameTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}

func (d *DB) UpsertGameStack(ctx context.Context, titleOrig string, proposer GameProposer) (*GameStack, error) {
	normalized := NormalizeGameTitle(titleOrig)
	var stack GameStack
	var proposersJSON []byte

	err := d.pool.QueryRow(ctx,
		`SELECT id,game_title,game_title_orig,count,proposers,last_proposed_at
		 FROM game_stacks WHERE game_title=$1`, normalized,
	).Scan(&stack.ID, &stack.GameTitle, &stack.GameTitleOrig, &stack.Count, &proposersJSON, &stack.LastProposed)

	if err == pgx.ErrNoRows {
		newProposers, _ := json.Marshal([]GameProposer{proposer})
		err2 := d.pool.QueryRow(ctx,
			`INSERT INTO game_stacks(game_title,game_title_orig,count,proposers)
			 VALUES($1,$2,1,$3)
			 RETURNING id,game_title,game_title_orig,count,proposers,last_proposed_at`,
			normalized, titleOrig, newProposers,
		).Scan(&stack.ID, &stack.GameTitle, &stack.GameTitleOrig, &stack.Count, &proposersJSON, &stack.LastProposed)
		if err2 != nil {
			return nil, err2
		}
	} else if err != nil {
		return nil, err
	} else {
		var proposers []GameProposer
		_ = json.Unmarshal(proposersJSON, &proposers)
		proposers = append(proposers, proposer)
		newBytes, _ := json.Marshal(proposers)
		err2 := d.pool.QueryRow(ctx,
			`UPDATE game_stacks SET count=count+1, proposers=$1, last_proposed_at=NOW()
			 WHERE game_title=$2
			 RETURNING id,game_title,game_title_orig,count,proposers,last_proposed_at`,
			newBytes, normalized,
		).Scan(&stack.ID, &stack.GameTitle, &stack.GameTitleOrig, &stack.Count, &proposersJSON, &stack.LastProposed)
		if err2 != nil {
			return nil, err2
		}
	}
	_ = json.Unmarshal(proposersJSON, &stack.Proposers)
	return &stack, nil
}

func (d *DB) GetTopGameStacks(ctx context.Context, limit int) ([]*GameStack, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT id,game_title,game_title_orig,count,proposers,last_proposed_at
		 FROM game_stacks ORDER BY count DESC, last_proposed_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*GameStack
	for rows.Next() {
		s := &GameStack{}
		var pj []byte
		if err := rows.Scan(&s.ID, &s.GameTitle, &s.GameTitleOrig, &s.Count, &pj, &s.LastProposed); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(pj, &s.Proposers)
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─── Streamer pending ─────────────────────────────────────────────────────────

func (d *DB) SetPending(ctx context.Context, streamerID int64, action string, targetUserID int64, extra string) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO streamer_pending(streamer_id,action,target_user_id,extra)
		 VALUES($1,$2,$3,$4)
		 ON CONFLICT(streamer_id) DO UPDATE SET action=EXCLUDED.action,
		 	target_user_id=EXCLUDED.target_user_id, extra=EXCLUDED.extra, created_at=NOW()`,
		streamerID, action, targetUserID, extra)
	return err
}

func (d *DB) GetPending(ctx context.Context, streamerID int64) (action string, targetUserID int64, extra string, err error) {
	err = d.pool.QueryRow(ctx,
		`SELECT action,target_user_id,extra FROM streamer_pending WHERE streamer_id=$1`, streamerID,
	).Scan(&action, &targetUserID, &extra)
	if err == pgx.ErrNoRows {
		return "", 0, "", nil
	}
	return
}

func (d *DB) ClearPending(ctx context.Context, streamerID int64) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM streamer_pending WHERE streamer_id=$1`, streamerID)
	return err
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func scanProposals(rows pgx.Rows) ([]*models.Proposal, error) {
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