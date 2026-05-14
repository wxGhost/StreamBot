package models

import "time"

type ProposalType string

const (
	TypeGame   ProposalType = "game"
	TypeStream ProposalType = "stream"
	TypeAnon   ProposalType = "anon"
)

type ProposalStatus string

const (
	StatusActive   ProposalStatus = "active"
	StatusArchived ProposalStatus = "archived"
	StatusTop      ProposalStatus = "top"
)

type Proposal struct {
	ID        int
	UserID    *int64  // nil if anonymous
	Username  *string // nil if anonymous
	FirstName *string // nil if anonymous
	Type      ProposalType
	Content   string
	MessageID *int64 // message id in streamer chat
	Status    ProposalStatus
	Likes     int
	Dislikes  int
	CreatedAt time.Time
}

func (p *Proposal) TypeTag() string {
	switch p.Type {
	case TypeGame:
		return "#Игра"
	case TypeStream:
		return "#Предложение"
	case TypeAnon:
		return "#Анонимно"
	}
	return ""
}

func (p *Proposal) StatusTag() string {
	switch p.Status {
	case StatusArchived:
		return "#Архив"
	case StatusTop:
		return "#Топ"
	}
	return ""
}

func (p *Proposal) DisplayName() string {
	if p.UserID == nil {
		return "Анонимно"
	}
	if p.FirstName != nil && *p.FirstName != "" {
		return *p.FirstName
	}
	if p.Username != nil && *p.Username != "" {
		return "@" + *p.Username
	}
	return "Пользователь"
}

func (p *Proposal) ProfileLink() string {
	if p.UserID == nil {
		return ""
	}
	if p.Username != nil && *p.Username != "" {
		return "https://t.me/" + *p.Username
	}
	return ""
}
