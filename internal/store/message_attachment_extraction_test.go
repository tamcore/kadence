package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestMessageRepositoryUpdateChatAttachmentExtractionsIsOwnedAndScoped(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "lazy-extraction-owner", Email: "lazy-extraction-owner@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.Create(ctx, model.User{
		Username: "lazy-extraction-other", Email: "lazy-extraction-other@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "lazy extraction")
	if err != nil {
		t.Fatal(err)
	}
	userMessage, err := messages.AddChatUserInput(
		ctx, conversation.ID, owner.ID, model.ChatUserInput{
			Content: "deferred",
			Attachments: []model.MessageAttachment{
				{
					Filename: "deferred.md", MIME: "text/markdown",
					Kind: model.AttachmentKindDocument, RawBytes: []byte("raw deferred"),
				},
				{
					Filename: "chart.png", MIME: "image/png",
					Kind: model.AttachmentKindImage, RawBytes: []byte{1, 2, 3},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	extracted := append([]model.MessageAttachment(nil), userMessage.Attachments...)
	extracted[0].ExtractedMarkdown = "# Lazily extracted"

	_, err = messages.UpdateChatAttachmentExtractions(
		ctx, conversation.ID, userMessage.ID, other.ID, extracted,
	)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user update error = %v, want ErrNotFound", err)
	}

	updated, err := messages.UpdateChatAttachmentExtractions(
		ctx, conversation.ID, userMessage.ID, owner.ID, extracted,
	)
	if err != nil {
		t.Fatalf("UpdateChatAttachmentExtractions: %v", err)
	}
	if len(updated.Attachments) != 2 ||
		updated.Attachments[0].ExtractedMarkdown != "# Lazily extracted" ||
		!bytes.Equal(updated.Attachments[0].RawBytes, []byte("raw deferred")) ||
		updated.Attachments[1].ExtractedMarkdown != "" ||
		!bytes.Equal(updated.Attachments[1].RawBytes, []byte{1, 2, 3}) {
		t.Fatalf("updated attachment payloads = %+v", updated.Attachments)
	}
}
