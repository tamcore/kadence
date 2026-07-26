package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestMessageRepositoryChatUserInputOrderedRoundTrip(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	documents := store.NewDocumentRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "attachment-round-trip", Email: "attachment-round-trip@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "attachments")
	if err != nil {
		t.Fatal(err)
	}
	privateDocument, err := documents.Create(ctx, model.Document{
		OwnerUserID: &owner.ID, Scope: model.ScopePrivate, Filename: "training.md",
		Mime: "text/markdown", SourceType: model.DocSourceText, ExtractedMarkdown: "# Training",
	})
	if err != nil {
		t.Fatal(err)
	}
	publicDocument, err := documents.Create(ctx, model.Document{
		Scope: model.ScopePublic, Filename: "public-guide.pdf", Mime: "application/pdf",
		SourceType: model.DocSourcePDF, ExtractedMarkdown: "# Public guide",
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := messages.AddChatUserInput(ctx, conversation.ID, owner.ID, store.ChatUserInput{
		Content: "Compare these",
		Attachments: []model.MessageAttachment{
			{
				Filename: "chart.png", MIME: "image/png", Kind: model.AttachmentKindImage,
				RawBytes: []byte{1, 2, 3}, ImageWidth: intPtr(640), ImageHeight: intPtr(480),
			},
			{
				Filename: "notes.md", MIME: "text/markdown", Kind: model.AttachmentKindDocument,
				RawBytes: []byte("notes"), ExtractedMarkdown: "# Notes",
			},
		},
		DocumentIDs: []int64{publicDocument.ID, privateDocument.ID},
	})
	if err != nil {
		t.Fatalf("add chat user input: %v", err)
	}
	assertAttachmentMetadata(t, created.Attachments)
	assertAttachmentPayloads(t, created.Attachments)
	assertReferenceMetadata(t, created.DocumentReferences, publicDocument.ID, privateDocument.ID)

	listed, err := messages.ListByConversation(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed messages = %d, want 1", len(listed))
	}
	assertAttachmentMetadata(t, listed[0].Attachments)
	assertAttachmentPayloadsOmitted(t, listed[0].Attachments)
	assertReferenceMetadata(t, listed[0].DocumentReferences, publicDocument.ID, privateDocument.ID)

	history, err := messages.ListChatHistory(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("list provider chat history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("provider history messages = %d, want 1", len(history))
	}
	assertAttachmentMetadata(t, history[0].Attachments)
	assertAttachmentPayloadsOmitted(t, history[0].Attachments)
	assertReferenceMetadata(
		t, history[0].DocumentReferences, publicDocument.ID, privateDocument.ID,
	)

	got, err := messages.GetByID(ctx, conversation.ID, created.ID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	assertAttachmentMetadata(t, got.Attachments)
	assertAttachmentPayloadsOmitted(t, got.Attachments)
	assertReferenceMetadata(t, got.DocumentReferences, publicDocument.ID, privateDocument.ID)
}

func TestMessageRepositoryLoadsScopedChatAttachmentPayloadsOnDemand(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "attachment-payload-owner", Email: "attachment-payload-owner@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "payloads")
	if err != nil {
		t.Fatal(err)
	}
	first, err := messages.AddChatUserInput(ctx, conversation.ID, owner.ID, store.ChatUserInput{
		Content: "first",
		Attachments: []model.MessageAttachment{{
			Filename: "first.md", MIME: "text/markdown",
			Kind: model.AttachmentKindDocument, RawBytes: []byte("first raw"),
			ExtractedMarkdown: "# First", ExtractionComplete: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := messages.AddChatUserInput(ctx, conversation.ID, owner.ID, store.ChatUserInput{
		Content: "second",
		Attachments: []model.MessageAttachment{{
			Filename: "second.png", MIME: "image/png",
			Kind: model.AttachmentKindImage, RawBytes: []byte{1, 2, 3},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	payloads, err := messages.LoadChatAttachmentPayloads(
		ctx, conversation.ID, []int64{second.ID},
	)
	if err != nil {
		t.Fatalf("load attachment payloads: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("payload message count = %d, want 1", len(payloads))
	}
	if _, ok := payloads[first.ID]; ok {
		t.Fatalf("unrequested first payload was loaded: %+v", payloads[first.ID])
	}
	if got := payloads[second.ID]; len(got) != 1 ||
		!bytes.Equal(got[0].RawBytes, []byte{1, 2, 3}) ||
		got[0].Ordinal != 0 {
		t.Fatalf("second payload = %+v", got)
	}

	payloads, err = messages.LoadChatAttachmentPayloads(
		ctx, conversation.ID, []int64{first.ID},
	)
	if err != nil {
		t.Fatalf("load document attachment payload: %v", err)
	}
	if got := payloads[first.ID]; len(got) != 1 ||
		len(got[0].RawBytes) != 0 ||
		got[0].ExtractedMarkdown != "# First" ||
		got[0].PayloadBytes != int64(len("# First")) {
		t.Fatalf("document provider payload = %+v", got)
	}
}

func TestMessageRepositoryRejectsCrossConversationAttachmentPayloadID(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "attachment-payload-scope", Email: "attachment-payload-scope@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstConversation, err := conversations.Create(ctx, owner.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	secondConversation, err := conversations.Create(ctx, owner.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := messages.AddChatUserInput(
		ctx, secondConversation.ID, owner.ID, store.ChatUserInput{
			Content: "foreign",
			Attachments: []model.MessageAttachment{{
				Filename: "foreign.png", MIME: "image/png",
				Kind: model.AttachmentKindImage, RawBytes: []byte{9},
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = messages.LoadChatAttachmentPayloads(
		ctx, firstConversation.ID, []int64{foreign.ID},
	)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-conversation payload error = %v, want ErrNotFound", err)
	}
}

func TestMessageRepositoryChatUserInputRollsBackInvalidReference(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	documents := store.NewDocumentRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "attachment-rollback", Email: "attachment-rollback@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.Create(ctx, model.User{
		Username: "attachment-rollback-other", Email: "attachment-rollback-other@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "rollback")
	if err != nil {
		t.Fatal(err)
	}
	invisibleDocument, err := documents.Create(ctx, model.Document{
		OwnerUserID: &other.ID, Scope: model.ScopePrivate, Filename: "private.md",
		Mime: "text/markdown", SourceType: model.DocSourceText, ExtractedMarkdown: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = messages.AddChatUserInput(ctx, conversation.ID, owner.ID, store.ChatUserInput{
		Content: "must roll back",
		Attachments: []model.MessageAttachment{{
			Filename: "proof.png", MIME: "image/png", Kind: model.AttachmentKindImage,
			RawBytes: []byte{9},
		}},
		DocumentIDs: []int64{invisibleDocument.ID},
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid reference err = %v, want ErrNotFound", err)
	}

	assertMessageRelationCounts(t, pool, conversation.ID, 0, 0, 0)
}

func TestMessageRepositoryNewConversationAndFirstInputRollBackTogether(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "first-input-rollback", Email: "first-input-rollback@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = messages.CreateConversationWithChatUserInput(
		ctx, owner.ID, "must roll back", model.ChatUserInput{
			Content: "first rich input",
			Attachments: []model.MessageAttachment{{
				Filename: "invalid.bin", MIME: "application/octet-stream",
				Kind: "invalid-kind", RawBytes: []byte{1},
			}},
		},
	)
	if err == nil {
		t.Fatal("invalid first rich input was accepted")
	}

	list, err := conversations.ListByUser(ctx, owner.ID)
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("failed first input left conversations behind: %+v", list)
	}
}

func TestMessageRepositoryDocumentReferenceSnapshotSurvivesDelete(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	documents := store.NewDocumentRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "reference-snapshot", Email: "reference-snapshot@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	document, err := documents.Create(ctx, model.Document{
		OwnerUserID: &owner.ID, Scope: model.ScopePrivate, Filename: "deleted-later.pdf",
		Mime: "application/pdf", SourceType: model.DocSourcePDF, ExtractedMarkdown: "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := messages.AddChatUserInput(ctx, conversation.ID, owner.ID, store.ChatUserInput{
		Content: "remember this", DocumentIDs: []int64{document.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := documents.Delete(ctx, document.ID, owner.ID); err != nil {
		t.Fatal(err)
	}

	got, err := messages.GetByID(ctx, conversation.ID, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DocumentReferences) != 1 {
		t.Fatalf("references = %+v", got.DocumentReferences)
	}
	reference := got.DocumentReferences[0]
	if reference.DocumentID != nil || reference.Available ||
		reference.Filename != "deleted-later.pdf" || reference.Scope != model.ScopePrivate {
		t.Fatalf("unavailable reference = %+v", reference)
	}
}

func TestMessageRepositoryConversationDeleteCascadesInputRelations(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	documents := store.NewDocumentRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "attachment-cascade", Email: "attachment-cascade@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "cascade")
	if err != nil {
		t.Fatal(err)
	}
	document, err := documents.Create(ctx, model.Document{
		OwnerUserID: &owner.ID, Scope: model.ScopePrivate, Filename: "source.md",
		Mime: "text/markdown", SourceType: model.DocSourceText, ExtractedMarkdown: "source",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.AddChatUserInput(ctx, conversation.ID, owner.ID, store.ChatUserInput{
		Content: "delete me",
		Attachments: []model.MessageAttachment{{
			Filename: "image.webp", MIME: "image/webp", Kind: model.AttachmentKindImage,
			RawBytes: []byte{4, 5},
		}},
		DocumentIDs: []int64{document.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := conversations.Delete(ctx, conversation.ID, owner.ID); err != nil {
		t.Fatal(err)
	}

	assertMessageRelationCounts(t, pool, conversation.ID, 0, 0, 0)
}

func TestMessageRepositoryEditPreservesTargetInputAndDeletesSuffixRelations(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	documents := store.NewDocumentRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "attachment-edit", Email: "attachment-edit@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "edit")
	if err != nil {
		t.Fatal(err)
	}
	document, err := documents.Create(ctx, model.Document{
		OwnerUserID: &owner.ID, Scope: model.ScopePrivate, Filename: "plan.md",
		Mime: "text/markdown", SourceType: model.DocSourceText, ExtractedMarkdown: "plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := messages.AddChatUserInput(ctx, conversation.ID, owner.ID, store.ChatUserInput{
		Content: "original",
		Attachments: []model.MessageAttachment{{
			Filename: "target.md", MIME: "text/markdown", Kind: model.AttachmentKindDocument,
			RawBytes: []byte("target"), ExtractedMarkdown: "# Target",
		}},
		DocumentIDs: []int64{document.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.Add(ctx, conversation.ID, model.MsgRoleAssistant, "answer"); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.AddChatUserInput(ctx, conversation.ID, owner.ID, store.ChatUserInput{
		Content: "later",
		Attachments: []model.MessageAttachment{{
			Filename: "later.png", MIME: "image/png", Kind: model.AttachmentKindImage,
			RawBytes: []byte{2},
		}},
		DocumentIDs: []int64{document.ID},
	}); err != nil {
		t.Fatal(err)
	}

	edited, err := messages.EditAndRewind(ctx, conversation.ID, target.ID, owner.ID, "edited")
	if err != nil {
		t.Fatalf("edit and rewind: %v", err)
	}
	if edited.Content != "edited" || len(edited.Attachments) != 1 ||
		edited.Attachments[0].Filename != "target.md" ||
		!bytes.Equal(edited.Attachments[0].RawBytes, []byte("target")) ||
		edited.Attachments[0].ExtractedMarkdown != "# Target" ||
		len(edited.DocumentReferences) != 1 {
		t.Fatalf("edited message lost input metadata: %+v", edited)
	}
	assertMessageRelationCounts(t, pool, conversation.ID, 1, 1, 1)
}

func TestMessageRepositoryRegenerateReusesPrecedingUserInput(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	documents := store.NewDocumentRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "attachment-regenerate", Email: "attachment-regenerate@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "regenerate")
	if err != nil {
		t.Fatal(err)
	}
	document, err := documents.Create(ctx, model.Document{
		OwnerUserID: &owner.ID, Scope: model.ScopePrivate, Filename: "workout.md",
		Mime: "text/markdown", SourceType: model.DocSourceText, ExtractedMarkdown: "workout",
	})
	if err != nil {
		t.Fatal(err)
	}
	userMessage, err := messages.AddChatUserInput(ctx, conversation.ID, owner.ID, store.ChatUserInput{
		Content: "coach this",
		Attachments: []model.MessageAttachment{{
			Filename: "run.md", MIME: "text/markdown", Kind: model.AttachmentKindDocument,
			RawBytes: []byte("run"), ExtractedMarkdown: "# Run",
		}},
		DocumentIDs: []int64{document.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	assistantMessage, err := messages.Add(ctx, conversation.ID, model.MsgRoleAssistant, "first answer")
	if err != nil {
		t.Fatal(err)
	}

	prompt, err := messages.RegenerateAndRewind(ctx, conversation.ID, assistantMessage.ID, owner.ID)
	if err != nil {
		t.Fatalf("regenerate and rewind: %v", err)
	}
	if prompt.ID != userMessage.ID || len(prompt.Attachments) != 1 ||
		prompt.Attachments[0].Filename != "run.md" ||
		!bytes.Equal(prompt.Attachments[0].RawBytes, []byte("run")) ||
		prompt.Attachments[0].ExtractedMarkdown != "# Run" ||
		len(prompt.DocumentReferences) != 1 {
		t.Fatalf("regenerate prompt lost input metadata: %+v", prompt)
	}
	assertMessageRelationCounts(t, pool, conversation.ID, 1, 1, 1)
}

func TestMessageRepositoryChatUserInputEnforcesConversationOwner(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "attachment-owner", Email: "attachment-owner@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.Create(ctx, model.User{
		Username: "attachment-other", Email: "attachment-other@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "owned")
	if err != nil {
		t.Fatal(err)
	}

	_, err = messages.AddChatUserInput(ctx, conversation.ID, other.ID, store.ChatUserInput{
		Content: "hijack",
		Attachments: []model.MessageAttachment{{
			Filename: "hijack.png", MIME: "image/png", Kind: model.AttachmentKindImage,
			RawBytes: []byte{3},
		}},
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner add err = %v, want ErrNotFound", err)
	}
	assertMessageRelationCounts(t, pool, conversation.ID, 0, 0, 0)
}

func TestMessageRepositoryChatUserInputDeleteRaceRollsBack(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	documents := store.NewDocumentRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "reference-delete-race", Email: "reference-delete-race@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "delete race")
	if err != nil {
		t.Fatal(err)
	}
	document, err := documents.Create(ctx, model.Document{
		OwnerUserID: &owner.ID, Scope: model.ScopePrivate, Filename: "racing.md",
		Mime: "text/markdown", SourceType: model.DocSourceText, ExtractedMarkdown: "race",
	})
	if err != nil {
		t.Fatal(err)
	}

	deleteTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = deleteTx.Rollback(ctx) }()
	if _, err := deleteTx.Exec(ctx, `DELETE FROM documents WHERE id = $1`, document.ID); err != nil {
		t.Fatal(err)
	}

	addCtx, cancelAdd := context.WithCancel(ctx)
	defer cancelAdd()
	result := make(chan error, 1)
	go func() {
		_, addErr := messages.AddChatUserInput(addCtx, conversation.ID, owner.ID, store.ChatUserInput{
			Content: "race",
			Attachments: []model.MessageAttachment{{
				Filename: "race.png", MIME: "image/png", Kind: model.AttachmentKindImage,
				RawBytes: []byte{8},
			}},
			DocumentIDs: []int64{document.ID},
		})
		result <- addErr
	}()

	waitForBlockedDocumentReferenceQuery(t, pool)
	if err := deleteTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-result:
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("delete-race add err = %v, want ErrNotFound", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for delete-race add")
	}
	assertMessageRelationCounts(t, pool, conversation.ID, 0, 0, 0)
}

func TestMessageRepositoryChatUserInputMapsDocumentForeignKeyViolation(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	documents := store.NewDocumentRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "reference-fk", Email: "reference-fk@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "foreign key")
	if err != nil {
		t.Fatal(err)
	}
	document, err := documents.Create(ctx, model.Document{
		OwnerUserID: &owner.ID, Scope: model.ScopePrivate, Filename: "fk.md",
		Mime: "text/markdown", SourceType: model.DocSourceText, ExtractedMarkdown: "fk",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(ctx, `
		CREATE FUNCTION test_message_reference_fk_violation() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION USING
				ERRCODE = '23503',
				CONSTRAINT = 'message_document_references_document_id_fkey';
			RETURN NEW;
		END
		$$
	`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		CREATE TRIGGER test_message_reference_fk_violation
		BEFORE INSERT ON message_document_references
		FOR EACH ROW EXECUTE FUNCTION test_message_reference_fk_violation()
	`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, cleanupErr := pool.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS test_message_reference_fk_violation
				ON message_document_references
		`)
		if cleanupErr != nil {
			t.Errorf("clean up FK violation trigger: %v", cleanupErr)
		}
		_, cleanupErr = pool.Exec(
			context.Background(),
			`DROP FUNCTION IF EXISTS test_message_reference_fk_violation()`,
		)
		if cleanupErr != nil {
			t.Errorf("clean up FK violation function: %v", cleanupErr)
		}
	})

	_, err = messages.AddChatUserInput(ctx, conversation.ID, owner.ID, store.ChatUserInput{
		Content: "map FK", DocumentIDs: []int64{document.ID},
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign-key violation err = %v, want ErrNotFound", err)
	}
	assertMessageRelationCounts(t, pool, conversation.ID, 0, 0, 0)
}

func TestMessageAttachmentRejectsMismatchedSize(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "attachment-size", Email: "attachment-size@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "size")
	if err != nil {
		t.Fatal(err)
	}
	message, err := messages.Add(ctx, conversation.ID, model.MsgRoleUser, "size")
	if err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO message_attachments (
		     message_id, filename, mime_type, kind, size_bytes, raw_bytes, ordinal
		 ) VALUES ($1, 'wrong.bin', 'application/octet-stream', 'document', 99, $2, 0)`,
		message.ID, []byte{1},
	)
	if err == nil {
		t.Fatal("mismatched attachment size was accepted")
	}
}

func assertAttachmentMetadata(t *testing.T, attachments []model.MessageAttachment) {
	t.Helper()
	if len(attachments) != 2 {
		t.Fatalf("attachments = %+v", attachments)
	}
	if attachments[0].Filename != "chart.png" || attachments[0].MIME != "image/png" ||
		attachments[0].Kind != model.AttachmentKindImage || attachments[0].SizeBytes != 3 ||
		attachments[0].Ordinal != 0 || attachments[0].ImageWidth == nil ||
		*attachments[0].ImageWidth != 640 || attachments[0].ImageHeight == nil ||
		*attachments[0].ImageHeight != 480 {
		t.Fatalf("first attachment metadata = %+v", attachments[0])
	}
	if attachments[1].Filename != "notes.md" || attachments[1].Kind != model.AttachmentKindDocument ||
		attachments[1].SizeBytes != 5 || attachments[1].Ordinal != 1 {
		t.Fatalf("second attachment metadata = %+v", attachments[1])
	}
}

func assertAttachmentPayloads(t *testing.T, attachments []model.MessageAttachment) {
	t.Helper()
	if !bytes.Equal(attachments[0].RawBytes, []byte{1, 2, 3}) ||
		attachments[0].ExtractedMarkdown != "" ||
		!bytes.Equal(attachments[1].RawBytes, []byte("notes")) ||
		attachments[1].ExtractedMarkdown != "# Notes" {
		t.Fatalf("attachment payloads = %+v", attachments)
	}
}

func assertAttachmentPayloadsOmitted(t *testing.T, attachments []model.MessageAttachment) {
	t.Helper()
	if attachments[0].RawBytes != nil || attachments[0].ExtractedMarkdown != "" ||
		attachments[1].RawBytes != nil || attachments[1].ExtractedMarkdown != "" {
		t.Fatalf("message hydration must omit attachment payloads: %+v", attachments)
	}
}

func assertReferenceMetadata(
	t *testing.T, references []model.MessageDocumentReference, publicID, privateID int64,
) {
	t.Helper()
	if len(references) != 2 {
		t.Fatalf("references = %+v", references)
	}
	if references[0].DocumentID == nil || *references[0].DocumentID != publicID ||
		references[0].Filename != "public-guide.pdf" || references[0].Scope != model.ScopePublic ||
		references[0].Ordinal != 0 || !references[0].Available {
		t.Fatalf("first reference = %+v", references[0])
	}
	if references[1].DocumentID == nil || *references[1].DocumentID != privateID ||
		references[1].Filename != "training.md" || references[1].Scope != model.ScopePrivate ||
		references[1].Ordinal != 1 || !references[1].Available {
		t.Fatalf("second reference = %+v", references[1])
	}
}

func assertMessageRelationCounts(
	t *testing.T, pool *pgxpool.Pool, conversationID string,
	wantMessages, wantAttachments, wantReferences int,
) {
	t.Helper()
	var messageCount, attachmentCount, referenceCount int
	err := pool.QueryRow(context.Background(),
		`SELECT
		    count(DISTINCT m.id),
		    count(DISTINCT a.id),
		    count(DISTINCT r.id)
		   FROM conversations c
		   LEFT JOIN messages m ON m.conversation_id = c.id
		   LEFT JOIN message_attachments a ON a.message_id = m.id
		   LEFT JOIN message_document_references r ON r.message_id = m.id
		  WHERE c.id = $1::uuid`,
		conversationID,
	).Scan(&messageCount, &attachmentCount, &referenceCount)
	if err != nil {
		t.Fatalf("count message relations: %v", err)
	}
	if messageCount != wantMessages || attachmentCount != wantAttachments || referenceCount != wantReferences {
		t.Fatalf(
			"relation counts = messages:%d attachments:%d references:%d, want %d/%d/%d",
			messageCount, attachmentCount, referenceCount, wantMessages, wantAttachments, wantReferences,
		)
	}
}

func waitForBlockedDocumentReferenceQuery(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := pool.QueryRow(context.Background(), `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_stat_activity
				 WHERE pid <> pg_backend_pid()
				   AND state = 'active'
				   AND wait_event_type = 'Lock'
				   AND (
				       position('FROM documents' IN query) > 0
				       OR position('INSERT INTO message_document_references' IN query) > 0
				   )
			)
		`).Scan(&waiting)
		if err != nil {
			t.Fatalf("inspect blocked document-reference query: %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("document-reference query did not block on concurrent delete")
}

func intPtr(value int) *int {
	return &value
}
