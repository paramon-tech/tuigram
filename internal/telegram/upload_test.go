package telegram

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

func uploadPhoto(t *testing.T) (string, []byte) {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 20, 10))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "photo with spaces.png")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path, data.Bytes()
}

func TestSendAttachmentUploadsActualBytesBeforeSendingCaption(t *testing.T) {
	path, original := uploadPhoto(t)
	var uploaded bytes.Buffer
	var fileID int64
	sends := 0
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		switch request := input.(type) {
		case *tg.UploadSaveFilePartRequest:
			if request.FileID == 0 || request.FilePart != 0 {
				t.Fatalf("invalid upload request: %+v", request)
			}
			fileID = request.FileID
			uploaded.Write(request.Bytes)
			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		case *tg.MessagesSendMediaRequest:
			sends++
			if !bytes.Equal(uploaded.Bytes(), original) {
				t.Fatal("sent before the original photo was completely uploaded")
			}
			peer := request.Peer.(*tg.InputPeerChannel)
			photo := request.Media.(*tg.InputMediaUploadedPhoto)
			file := photo.File.(*tg.InputFile)
			if peer.ChannelID != 2 || peer.AccessHash != 456 || request.Message != "hello with spaces 🙂" || request.RandomID == 0 || file.ID != fileID || file.Name != "photo with spaces.png" || file.Parts != 1 {
				t.Fatalf("send lost metadata: %+v, %+v", request, file)
			}
			output.(*tg.UpdatesBox).Updates = &tg.Updates{}
		default:
			t.Fatalf("unexpected RPC: %T", input)
		}
		return nil
	})
	if err := c.SendAttachment(context.Background(), core.Chat{ID: "channel:2"}, core.Attachment{Path: path, Caption: "hello with spaces 🙂"}); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatalf("sent %d times", sends)
	}
}

func TestSendAttachmentDoesNotSendFailedChangedOrCanceledUpload(t *testing.T) {
	for _, failure := range []string{"upload failed", "changed", "canceled", "invalid source", "unknown peer"} {
		t.Run(failure, func(t *testing.T) {
			path, _ := uploadPhoto(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				calls++
				if _, ok := input.(*tg.UploadSaveFilePartRequest); !ok {
					t.Fatalf("failed upload sent media: %T", input)
				}
				switch failure {
				case "upload failed":
					return errors.New("connection lost")
				case "changed":
					if err := os.Truncate(path, 0); err != nil {
						t.Fatal(err)
					}
				case "canceled":
					cancel()
				}
				output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
				return nil
			})
			chat := core.Chat{ID: "user:1"}
			if failure == "invalid source" {
				path = t.TempDir()
			}
			if failure == "unknown peer" {
				chat.ID = "user:9999"
			}
			if err := c.SendAttachment(ctx, chat, core.Attachment{Path: path}); err == nil {
				t.Fatal("upload reported success")
			} else if failure == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if calls > 1 || ((failure == "invalid source" || failure == "unknown peer") && calls > 0) {
				t.Fatalf("unexpected RPC count: %d", calls)
			}
		})
	}
}

func TestSendAttachmentDoesNotRetrySendingAfterAmbiguousError(t *testing.T) {
	path, _ := uploadPhoto(t)
	sends := 0
	want := errors.New("response lost")
	c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch input.(type) {
		case *tg.UploadSaveFilePartRequest:
			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
			return nil
		case *tg.MessagesSendMediaRequest:
			sends++
			return want
		default:
			t.Fatalf("unexpected RPC: %T", input)
			return nil
		}
	})
	if err := c.SendAttachment(context.Background(), core.Chat{ID: "user:1"}, core.Attachment{Path: path}); !errors.Is(err, want) || sends != 1 {
		t.Fatalf("ambiguous send error: %v; sends=%d", err, sends)
	}
}

func TestSendVideoAttachmentStreamsLargeFilesAndPreservesVideoMetadata(t *testing.T) {
	// The fixture is a generated 2.5-second blue H.264 clip. Expanding its mdat
	// with a sparse file exercises the big-file RPC without a large fixture.
	data, err := os.ReadFile("../core/testdata/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	const size = 11 << 20
	mdat := bytes.Index(data, []byte("mdat")) - 4
	binary.BigEndian.PutUint32(data[mdat:], size-uint32(mdat))
	path := filepath.Join(t.TempDir(), "my video.mp4")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	var uploaded, parts, sends int
	var fileID int64
	c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch request := input.(type) {
		case *tg.UploadSaveBigFilePartRequest:
			if request.FilePart != parts || len(request.Bytes) > 512<<10 || request.FileTotalParts <= 1 || (parts > 0 && request.FileID != fileID) {
				t.Errorf("invalid big file part: index=%d bytes=%d total=%d", request.FilePart, len(request.Bytes), request.FileTotalParts)
				return errors.New("invalid big file part")
			}
			fileID = request.FileID
			parts++
			uploaded += len(request.Bytes)
			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		case *tg.MessagesSendMediaRequest:
			sends++
			video := request.Media.(*tg.InputMediaUploadedDocument)
			file := video.File.(*tg.InputFileBig)
			name := video.Attributes[0].(*tg.DocumentAttributeFilename)
			attr := video.Attributes[1].(*tg.DocumentAttributeVideo)
			if uploaded != size || file.ID != fileID || file.Parts != parts || name.FileName != "my video.mp4" || video.MimeType != "video/mp4" || attr.Duration != 2.5 || attr.W != 320 || attr.H != 240 || !attr.SupportsStreaming || request.Message != "" {
				t.Errorf("lost video metadata or streamed bytes: %+v %+v uploaded=%d", file, attr, uploaded)
				return errors.New("invalid video request")
			}
			output.(*tg.UpdatesBox).Updates = &tg.Updates{}
		default:
			t.Errorf("unexpected video RPC: %T", input)
			return errors.New("unexpected video RPC")
		}
		return nil
	})
	if err := c.SendAttachment(context.Background(), core.Chat{ID: "user:1"}, core.Attachment{Path: path, Kind: "video"}); err != nil || sends != 1 {
		t.Fatalf("video send: %v; sends=%d", err, sends)
	}
}

func TestSendDocumentKeepsPhotoAsFile(t *testing.T) {
	path, original := uploadPhoto(t)
	var uploaded bytes.Buffer
	c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch request := input.(type) {
		case *tg.UploadSaveFilePartRequest:
			uploaded.Write(request.Bytes)
			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		case *tg.MessagesSendMediaRequest:
			file, ok := request.Media.(*tg.InputMediaUploadedDocument)
			if !ok || !file.ForceFile || len(file.Attributes) != 1 || file.MimeType != "image/png" || !bytes.Equal(uploaded.Bytes(), original) {
				t.Fatalf("send-as-file lost original: %+v", request.Media)
			}
			output.(*tg.UpdatesBox).Updates = &tg.Updates{}
		default:
			t.Fatalf("unexpected request %T", input)
		}
		return nil
	})
	if err := c.SendAttachment(context.Background(), core.Chat{ID: "user:1"}, core.Attachment{Path: path, Kind: "document"}); err != nil {
		t.Fatal(err)
	}
}

func TestSendAlbumPreparesMediaBeforeSinglePublish(t *testing.T) {
	for _, kind := range []string{"photo", "document"} {
		t.Run(kind, func(t *testing.T) {
			path, _ := uploadPhoto(t)
			parts, prepared, sends := 0, 0, 0
			c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				switch request := input.(type) {
				case *tg.UploadSaveFilePartRequest:
					parts++
					output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
				case *tg.MessagesUploadMediaRequest:
					prepared++
					if parts != prepared {
						t.Fatal("prepared before file upload")
					}
					if kind == "photo" {
						if _, ok := request.Media.(*tg.InputMediaUploadedPhoto); !ok {
							t.Fatalf("expected photo: %T", request.Media)
						}
						output.(*tg.MessageMediaBox).MessageMedia = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(prepared), AccessHash: 99, FileReference: []byte("reference")}}
					} else {
						if document, ok := request.Media.(*tg.InputMediaUploadedDocument); !ok || !document.ForceFile {
							t.Fatalf("expected forced document: %T", request.Media)
						}
						output.(*tg.MessageMediaBox).MessageMedia = &tg.MessageMediaDocument{Document: &tg.Document{ID: int64(prepared), AccessHash: 99, FileReference: []byte("reference")}}
					}
				case *tg.MessagesSendMultiMediaRequest:
					sends++
					if prepared != 2 || len(request.MultiMedia) != 2 || request.MultiMedia[0].Message != "Album caption" || request.MultiMedia[1].Message != "" {
						t.Fatalf("incomplete album publish: %+v", request)
					}
					if request.MultiMedia[0].RandomID == 0 || request.MultiMedia[0].RandomID == request.MultiMedia[1].RandomID {
						t.Fatal("album items need distinct nonzero random IDs")
					}
					for i, item := range request.MultiMedia {
						var id, hash int64
						var reference []byte
						switch media := item.Media.(type) {
						case *tg.InputMediaPhoto:
							photo := media.ID.(*tg.InputPhoto)
							id, hash, reference = photo.ID, photo.AccessHash, photo.FileReference
						case *tg.InputMediaDocument:
							document := media.ID.(*tg.InputDocument)
							id, hash, reference = document.ID, document.AccessHash, document.FileReference
						default:
							t.Fatalf("published raw upload: %T", item.Media)
						}
						if id != int64(i+1) || hash != 99 || string(reference) != "reference" {
							t.Fatal("lost Telegram media references")
						}
					}
					output.(*tg.UpdatesBox).Updates = &tg.Updates{}
				default:
					t.Fatalf("unexpected request %T", input)
				}
				return nil
			})
			if err := c.SendAttachments(context.Background(), core.Chat{ID: "user:1"}, []core.Attachment{{Path: path, Kind: kind, Caption: "Album caption"}, {Path: path, Kind: kind}}); err != nil || sends != 1 {
				t.Fatalf("album send: %v, sends=%d", err, sends)
			}
		})
	}
}

func TestAlbumValidationFailureDoesNotUploadAnyFiles(t *testing.T) {
	path, _ := uploadPhoto(t)
	c := testClient(func(_ context.Context, input bin.Encoder, _ bin.Decoder) error {
		t.Fatalf("invalid later file triggered RPC %T", input)
		return nil
	})
	if err := c.SendAttachments(context.Background(), core.Chat{ID: "user:1"}, []core.Attachment{{Path: path}, {Path: t.TempDir()}}); err == nil {
		t.Fatal("accepted invalid later attachment")
	}
}

func TestAlbumFailureNeverPublishesPartialOrRetriesAmbiguousSend(t *testing.T) {
	for _, failure := range []string{"prepare", "changed earlier", "cancel", "send"} {
		t.Run(failure, func(t *testing.T) {
			path, _ := uploadPhoto(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prepared, sends := 0, 0
			c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				switch input.(type) {
				case *tg.UploadSaveFilePartRequest:
					output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
				case *tg.MessagesUploadMediaRequest:
					prepared++
					if prepared == 2 {
						switch failure {
						case "prepare":
							return errors.New("prepare failed")
						case "cancel":
							cancel()
						case "changed earlier":
							if err := os.Truncate(path, 1); err != nil {
								t.Fatal(err)
							}
						}
					}
					output.(*tg.MessageMediaBox).MessageMedia = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(prepared)}}
				case *tg.MessagesSendMultiMediaRequest:
					sends++
					return errors.New("response lost")
				default:
					t.Fatalf("unexpected request %T", input)
				}
				return nil
			})
			if err := c.SendAttachments(ctx, core.Chat{ID: "user:1"}, []core.Attachment{{Path: path}, {Path: path}}); err == nil {
				t.Fatal("failed album reported success")
			}
			want := 0
			if failure == "send" {
				want = 1
			}
			if sends != want {
				t.Fatalf("publishing requests=%d, want=%d", sends, want)
			}
		})
	}
}
