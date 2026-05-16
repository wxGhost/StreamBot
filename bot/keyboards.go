package bot

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// mainReplyKeyboard shown to users.
func mainReplyKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🎮 Предложить игру"),
			tgbotapi.NewKeyboardButton("💡 Предложения на стрим"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🕵️ Анонимно"),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}

// streamerReplyKeyboard shown to the streamer.
func streamerReplyKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📦 Архив"),
			tgbotapi.NewKeyboardButton("⭐ Топ"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📊 Статистика"),
			tgbotapi.NewKeyboardButton("🎮 Статистика игр"),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}

// proposalInlineKeyboard attached to each proposal in the streamer chat.
// If userID is nil (anon), the Block button is hidden.
func proposalInlineKeyboard(proposalID int, userID *int64) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📢 В канал", cbData("publish", proposalID)),
			tgbotapi.NewInlineKeyboardButtonData("⭐ В топ", cbData("top", proposalID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📦 В архив", cbData("archive", proposalID)),
			tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить", cbData("delete", proposalID)),
		),
	}

	// Block button only for non-anonymous proposals
	if userID != nil {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("ℹ️ Информация", cbData("info", proposalID)),
			tgbotapi.NewInlineKeyboardButtonData("🚫 Заблокировать", cbData("block", proposalID)),
		))
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("ℹ️ Информация", cbData("info", proposalID)),
		))
	}

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// channelVoteKeyboard attached to channel posts.
func channelVoteKeyboard(proposalID, likes, dislikes int) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("👍 %d", likes), cbData("like", proposalID)),
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("👎 %d", dislikes), cbData("dislike", proposalID)),
		),
	)
}
