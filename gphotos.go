package gphotos

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go4.org/syncutil/singleflight"
	"golang.org/x/time/rate"
	"google.golang.org/api/drive/v3"
)

// The maximum rate limit is 10 qps per IP address.
// The default value set in Google API Console is 1 qps per IP address.
var Rate = rate.NewLimiter(10, 1)

// Photos returns a channel which will receive all the photos metadata
// in batches.
//
// The client must be prepared with drive.DrivePhotosReadonlyScope
// ("https://www.googleapis.com/auth/drive.photos.readonly").
//
// If sinceToken is not empty, only the changed photos are returned.
//
// Returns a new token to watch future changes.
func Photos(ctx context.Context, client *http.Client, sinceToken string) (<-chan MaybePhotos, string, error) {
	srv, err := drive.New(client)
	if err != nil {
		return nil, "", err
	}

	sr, err := do(ctx, func() (interface{}, error) { return srv.Changes.GetStartPageToken().Do() })
	if err != nil {
		return nil, "", err
	}
	nextToken := sr.(*drive.StartPageToken).StartPageToken

	const fields = "nextPageToken, files(id,name,mimeType,description,starred,parents,properties,webContentLink,createdTime,modifiedTime,owners,originalFilename,imageMediaMetadata)"

	if sinceToken != "" {
		L := func(token string) (*drive.ChangeList, error) {
			I, err := do(ctx, func() (interface{}, error) {
				return srv.Changes.List(token).
					Fields(fields).
					Spaces("photos").
					PageSize(1000).
					IncludeRemoved(false).Do()
			})
			if I != nil {
				return I.(*drive.ChangeList), err
			}
			return nil, err
		}

		r, err := L(sinceToken)
		if err != nil {
			return nil, "", err
		}
		ch := make(chan MaybePhotos)
		go getChanges(ctx, ch, L, srv.Files, r)
		return ch, nextToken, err
	}

	listCall := srv.Files.List().
		Fields(fields).
		Spaces("photos").
		PageSize(1000)
	L := func(token string) (*drive.FileList, error) {
		listCall := listCall
		if token != "" {
			listCall = listCall.PageToken(token)
		}
		I, err := do(ctx, func() (interface{}, error) {
			return listCall.Do()
		})
		if I != nil {
			return I.(*drive.FileList), err
		}
		return nil, err
	}
	r, err := L("")
	if err != nil {
		return nil, nextToken, err
	}
	ch := make(chan MaybePhotos)
	go getPhotos(ctx, ch, L, srv.Files, r)

	return ch, nextToken, nil
}

func getPhotos(ctx context.Context, ch chan<- MaybePhotos, L func(string) (*drive.FileList, error), srv *drive.FilesService, r *drive.FileList) {
	defer close(ch)
	for {
		go func() {
			photos := make([]Photo, 0, len(r.Files))
			for _, f := range r.Files {
				p, err := fileAsPhoto(ctx, srv, f)
				photos = append(photos, p)
				if err != nil {
					ch <- MaybePhotos{Photos: photos, Err: err}
					return
				}
			}
			ch <- MaybePhotos{Photos: photos}
		}()

		if r.NextPageToken == "" {
			return
		}
		var err error
		if r, err = L(r.NextPageToken); err != nil {
			ch <- MaybePhotos{Err: err}
			return
		}
	}
}

func getChanges(ctx context.Context, ch chan<- MaybePhotos, L func(string) (*drive.ChangeList, error), srv *drive.FilesService, r *drive.ChangeList) {
	defer close(ch)
	for {
		go func() {
			photos := make([]Photo, 0, len(r.Changes))
			for _, c := range r.Changes {
				if c.File != nil {
					p, err := fileAsPhoto(ctx, srv, c.File)
					photos = append(photos, p)
					if err != nil {
						ch <- MaybePhotos{Photos: photos, Err: err}
						return
					}
				}
			}
			if len(photos) > 0 {
				ch <- MaybePhotos{Photos: photos}
			}
		}()
		if r.NextPageToken == "" {
			return
		}
		var err error
		if r, err = L(r.NextPageToken); err != nil {
			ch <- MaybePhotos{Err: err}
			return
		}
	}
}

// MaybeFiles may contain files slice, or an error.
type MaybePhotos struct {
	Photos []Photo
	Err    error
}

type Photo struct {
	ID                          string
	Name, MimeType, Description string
	Starred                     bool
	Parents                     []string
	Properties                  map[string]string
	WebContentLink              string
	CreatedTime, ModifiedTime   time.Time
	OriginalFilename            string
	drive.FileImageMediaMetadata
}

func fileAsPhoto(ctx context.Context, srv *drive.FilesService, f *drive.File) (Photo, error) {
	if f == nil {
		return Photo{}, nil
	}
	p := Photo{
		ID:               f.Id,
		Name:             f.Name,
		MimeType:         f.MimeType,
		Description:      f.Description,
		Starred:          f.Starred,
		Properties:       f.Properties,
		WebContentLink:   f.WebContentLink,
		OriginalFilename: f.OriginalFilename,
	}
	if f.ImageMediaMetadata != nil {
		p.FileImageMediaMetadata = *f.ImageMediaMetadata
	}
	if f.CreatedTime != "" {
		p.CreatedTime, _ = time.Parse(time.RFC3339, f.CreatedTime)
	}
	if f.ModifiedTime != "" {
		p.ModifiedTime, _ = time.Parse(time.RFC3339, f.ModifiedTime)
	}
	p.Parents = make([]string, len(f.Parents))
	var firstErr error
	for i, k := range f.Parents {
		pf, err := fileOf(ctx, srv, k)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		parts, err := pathOf(ctx, srv, pf)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		p.Parents[i] = "/" + strings.Join(parts, "/")
	}
	return p, firstErr
}

var (
	dirs      map[string]*drive.File
	dirSingle singleflight.Group
)

func init() {
	dirs = make(map[string]*drive.File)
}

func fileOf(ctx context.Context, srv *drive.FilesService, p string) (*drive.File, error) {
	r, err := dirSingle.Do(
		p,
		func() (interface{}, error) {
			if f := dirs[p]; f != nil {
				return f, nil
			}
			I, err := do(ctx, func() (interface{}, error) {
				return srv.Get(p).Fields("id, name, parents").Do()
			})
			if I != nil {
				f := I.(*drive.File)
				dirs[p] = f
				return f, nil
			}
			return nil, err
		})
	if err != nil {
		return nil, err
	}
	return r.(*drive.File), nil
}

func pathOf(ctx context.Context, srv *drive.FilesService, f *drive.File) ([]string, error) {
	nm := []string{f.Name}
	if len(f.Parents) == 0 {
		return nm, nil
	}
	p, err := fileOf(ctx, srv, f.Parents[0])
	if err != nil || p == nil {
		return nm, err
	}
	prev, err := pathOf(ctx, srv, p)
	return append(prev, nm...), err
}

func do(ctx context.Context, f func() (interface{}, error)) (interface{}, error) {
	for {
		if err := Rate.Wait(ctx); err != nil {
			return nil, err
		}
		resp, err := f()
		if err != nil {
			if strings.Contains(err.Error(), "Rate Limit Exceeded") {
				Rate.SetLimit(Rate.Limit() * 0.9)
				continue
			}
			return nil, err
		}
		return resp, err
	}
}
