package store_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestMessageRepositoryGetAttachmentForUserScopesWholePath(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := t.Context()

	owner, err := users.Create(ctx, model.User{
		Username: "delivery-owner", Email: "delivery-owner@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.Create(ctx, model.User{
		Username: "delivery-other", Email: "delivery-other@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	ownedConversation, err := conversations.Create(ctx, owner.ID, "owned")
	if err != nil {
		t.Fatal(err)
	}
	otherOwnedConversation, err := conversations.Create(ctx, owner.ID, "other owned")
	if err != nil {
		t.Fatal(err)
	}
	message, err := messages.AddChatUserInput(
		ctx, ownedConversation.ID, owner.ID, model.ChatUserInput{
			Content: "image",
			Attachments: []model.MessageAttachment{{
				Filename: "proof.png", MIME: "image/png", Kind: model.AttachmentKindImage,
				RawBytes: []byte("private-image-bytes"),
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	otherMessage, err := messages.AddChatUserInput(
		ctx, otherOwnedConversation.ID, owner.ID, model.ChatUserInput{
			Content: testOtherUsername,
			Attachments: []model.MessageAttachment{{
				Filename: "other.png", MIME: testMimePNG, Kind: model.AttachmentKindImage,
				RawBytes: []byte("other-bytes"),
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	got, err := messages.GetAttachmentForUser(
		ctx, owner.ID, ownedConversation.ID, message.ID, message.Attachments[0].ID,
	)
	if err != nil {
		t.Fatalf("get owned attachment: %v", err)
	}
	if got.ID != message.Attachments[0].ID ||
		got.MessageID != message.ID ||
		got.Filename != testProofPNGFilename ||
		got.MIME != testMimePNG ||
		got.Kind != model.AttachmentKindImage ||
		!bytes.Equal(got.RawBytes, []byte("private-image-bytes")) {
		t.Fatalf("attachment=%+v", got)
	}

	tests := []struct {
		name           string
		userID         int64
		conversationID string
		messageID      int64
		attachmentID   int64
	}{
		{
			name:   "wrong user",
			userID: other.ID, conversationID: ownedConversation.ID,
			messageID: message.ID, attachmentID: message.Attachments[0].ID,
		},
		{
			name:   "wrong conversation",
			userID: owner.ID, conversationID: otherOwnedConversation.ID,
			messageID: message.ID, attachmentID: message.Attachments[0].ID,
		},
		{
			name:   "wrong message",
			userID: owner.ID, conversationID: ownedConversation.ID,
			messageID: otherMessage.ID, attachmentID: message.Attachments[0].ID,
		},
		{
			name:   "wrong attachment",
			userID: owner.ID, conversationID: ownedConversation.ID,
			messageID: message.ID, attachmentID: otherMessage.Attachments[0].ID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, getErr := messages.GetAttachmentForUser(
				ctx, test.userID, test.conversationID, test.messageID, test.attachmentID,
			)
			if !errors.Is(getErr, store.ErrNotFound) {
				t.Fatalf("error=%v want ErrNotFound", getErr)
			}
		})
	}
}
