package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/psnehanshu/cleanpincode.in/internal/queries"
)

// Mount routes
func (s *Server) mountRoutes(app *fiber.App) {
	app.Get("/", func(c *fiber.Ctx) error {
		mostUpvoted, err := s.queries.MostUpvoted(c.Context(), queries.MostUpvotedParams{Limit: 5})
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError, "failed to get most upvoted pincodes")
		}

		mostDownvoted, err := s.queries.MostDownvoted(c.Context(), queries.MostDownvotedParams{Limit: 5})
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError, "failed to get most upvoted pincodes")
		}

		return c.Render("views/index", fiber.Map{"MostUpvoted": mostUpvoted, "MostDownvoted": mostDownvoted})
	})

	app.Get("/about", func(c *fiber.Ctx) error {
		return c.Render("views/about", nil)
	})

	app.Get("/contact", func(c *fiber.Ctx) error {
		return c.Render("views/contact", nil)
	})

	app.Get("/leaderboard", func(c *fiber.Ctx) error {
		mostUpvoted, err := s.queries.MostUpvoted(c.Context(), queries.MostUpvotedParams{Limit: 50})
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError, "failed to get most upvoted pincodes")
		}

		return c.Render("views/board", fiber.Map{"Board": mostUpvoted, "Title": "Leaderboard"})
	})

	app.Get("/looserboard", func(c *fiber.Ctx) error {
		mostDownvoted, err := s.queries.MostDownvoted(c.Context(), queries.MostDownvotedParams{Limit: 50})
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError, "failed to get MostDownvoted pincodes")
		}

		return c.Render("views/board", fiber.Map{"Board": mostDownvoted, "Title": "Looserboard"})
	})

	app.Get("/pincode", func(c *fiber.Ctx) error {
		page, limit := c.QueryInt("page", 1), c.QueryInt("limit", 100)
		if page < 1 {
			page = 1
		}
		if limit < 1 {
			limit = 1
		}
		offset := (page - 1) * limit

		pincodes, err := s.queries.GetPincodes(c.Context(), queries.GetPincodesParams{
			Limit: int32(limit), Offset: int32(offset),
		})
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError, "failed to get pincodes")
		}

		return c.Render("views/pincodes", fiber.Map{
			"Pincodes": pincodes,
			"Limit":    limit, "Page": page, "NextPage": page + 1, "PrevPage": page - 1,
		})
	})

	app.Get("/pincode/:pincode", func(c *fiber.Ctx) error {
		var pincode pgtype.Int4
		if pint, err := strconv.ParseInt(c.Params("pincode"), 10, 32); err != nil {
			return fiber.NewError(http.StatusBadRequest)
		} else {
			pincode.Int32 = int32(pint)
			pincode.Valid = true
		}

		pincodeResult, err := s.queries.GetByPincode(c.Context(), pincode)
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError, "failed to get pincode")
		}

		if len(pincodeResult) == 0 {
			return fiber.NewError(http.StatusNotFound, "pincode not found")
		}

		// Find all unique states, in 99.99% cases, there will be only one state
		statesMap := make(map[string]bool)
		states := make([]string, 0, len(pincodeResult))
		for _, p := range pincodeResult {
			if statesMap[p.Statename.String] {
				continue
			}
			statesMap[p.Statename.String] = true
			states = append(states, p.Statename.String)
		}

		// Calculate votes
		votes, err := s.queries.GetPincodeVotes(c.Context(), pincode.Int32)
		if err != nil {
			if err == pgx.ErrNoRows {
				votes.Pincode = pincode.Int32
			} else {
				return fiber.NewError(http.StatusInternalServerError, "failed to get pincode votes")
			}
		}

		return c.Render("views/pincode", fiber.Map{
			"PostOffices": pincodeResult,
			"Pincode":     pincode,
			"State":       strings.Join(states, "/"),
			"Votes":       votes,
			"TotalVotes":  votes.Upvotes - votes.Downvotes,
		})
	})

	app.Get("/vote", func(c *fiber.Ctx) error {
		pincode := c.QueryInt("pin")
		pincodeResult, err := s.queries.GetByPincode(c.Context(), pgtype.Int4{Int32: int32(pincode), Valid: true})
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError, "failed to get pincode")
		}

		if len(pincodeResult) == 0 {
			return fiber.NewError(http.StatusNotFound)
		}

		// Check logged in status
		user, err := s.getUserFromSession(c)
		if err != nil {
			if fErr, ok := err.(*fiber.Error); ok {
				return fErr
			} else {
				s.logger.Warnw("failed to get user from session", "error", err)
			}
		}

		// fetch existing vote
		var vote *queries.Vote
		if user != nil {
			// fetch existing vote
			v, err := s.queries.GetVote(c.Context(), queries.GetVoteParams{
				Pincode: int32(pincode), VoterID: user.ID,
			})
			if err != nil {
				if err != pgx.ErrNoRows {
					return fiber.NewError(http.StatusInternalServerError, "failed to get vote")
				}
			} else {
				vote = &v
			}
		}

		return c.Render("views/vote", fiber.Map{
			"Pincode":  pincode,
			"Info":     pincodeResult,
			"ClientID": os.Getenv("GOOGLE_CLIENT_ID"),
			"User":     user,
			"Vote":     vote,
		})
	})

	app.Post("/vote", func(c *fiber.Ctx) error {
		user, err := s.getUserFromSession(c)
		if err != nil {
			s.logger.Errorw("session error", "error", err)
			return fiber.NewError(http.StatusUnauthorized)
		}

		// Parse the multipart form
		form, err := c.MultipartForm()
		if err != nil {
			return err
		}

		// Extract vote type
		var voteType queries.VoteType
		{
			vt, ok := form.Value["vote_type"]
			if !ok || len(vt) == 0 || vt[0] == "" {
				return fiber.NewError(http.StatusBadRequest, "no vote type submitted")
			} else {
				switch vt[0] {
				case "upvote":
					voteType = queries.VoteTypeUp
				case "downvote":
					voteType = queries.VoteTypeDown
				default:
					return fiber.NewError(http.StatusBadRequest, fmt.Sprintf("invalid vote type: %s", vt[0]))
				}
			}
		}

		// extract pincode
		var pincode int
		{
			pincodes, ok := form.Value["pincode"]
			if !ok || len(pincodes) == 0 || pincodes[0] == "" {
				return fiber.NewError(http.StatusBadRequest, "no pincode submitted")
			} else {
				pincode, err = strconv.Atoi(pincodes[0])
				if err != nil {
					return fiber.NewError(http.StatusBadRequest, "pincode must be numeric")
				}
			}
		}

		// Fetch existing vote
		var vote *queries.Vote
		{
			v, err := s.queries.GetVote(c.Context(), queries.GetVoteParams{
				Pincode: int32(pincode), VoterID: user.ID,
			})
			if err != nil {
				if err != pgx.ErrNoRows {
					return err
				}
			} else {
				vote = &v
			}
		}

		// Record vote (with transaction)
		tx, err := s.db.Begin(c.Context())
		if err != nil {
			return err
		}
		qtx := s.queries.WithTx(tx)

		if vote == nil {
			// new vote
			v, err := qtx.CreateVote(c.Context(), queries.CreateVoteParams{
				Type: voteType, Pincode: int32(pincode), VoterID: user.ID,
			})
			if err != nil {
				return err
			}

			vote = &v
		} else {
			// update existing vote
			err := qtx.UpdateExistingVote(c.Context(), queries.UpdateExistingVoteParams{
				Type: voteType, ID: vote.ID,
			})
			if err != nil {
				return err
			}
		}

		// Get all uploaded pics
		pics := form.File["pics"]
		if err := s.uploadPicsForVote(pics, vote.ID); err != nil {
			// roll back vote
			if err := tx.Rollback(c.Context()); err != nil {
				return err
			}

			// end request with error
			return err
		}

		// Save vote
		if err := tx.Commit(c.Context()); err != nil {
			return err
		}

		return c.Redirect(fmt.Sprintf("/vote?pin=%d", pincode))
	})

	app.Post("/google-login", func(c *fiber.Ctx) error {
		// Extract credential from body
		var data map[string]interface{}
		if err := json.Unmarshal(c.Body(), &data); err != nil {
			return fiber.NewError(http.StatusBadRequest, "invalid json")
		}
		credential, ok := data["credential"].(string)
		if !ok {
			return fiber.NewError(http.StatusBadRequest, "invalid json")
		}

		set, err := jwk.Fetch(c.Context(), "https://www.googleapis.com/oauth2/v3/certs")
		if err != nil {
			s.logger.Errorw("failed to fetch jwk", "error", err)
			return fiber.NewError(http.StatusInternalServerError)
		}

		// Validate credential
		time.Sleep(2 * time.Second) // sleeping to prevent iat validation error
		token, err := jwt.Parse(
			[]byte(credential),
			jwt.WithKeySet(set),
			jwt.WithAudience(os.Getenv("GOOGLE_CLIENT_ID")),
			jwt.WithIssuer("https://accounts.google.com"),
		)
		if err != nil {
			s.logger.Errorw("failed to parse token", "error", err)
			return fiber.NewError(http.StatusUnauthorized)
		}

		user, err := s.getUserFromGoogleJwtToken(c, token)
		if user == nil {
			return nil
		}
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError)
		}

		// Set session
		sess, err := s.sessionStore.Get(c)
		if err != nil {
			s.logger.Errorw("failed to get session", "error", err)
			return fiber.NewError(http.StatusInternalServerError)
		}

		expiryDuration := time.Hour * 24 * 365
		expiresAt := time.Now().Add(expiryDuration)
		loginToken, err := generateLoginJWT(user, expiresAt)
		if err != nil {
			s.logger.Errorw("failed to generate login token", "error", err)
			return fiber.NewError(http.StatusInternalServerError)
		}

		sess.Set("logged-in-user", loginToken)
		sess.SetExpiry(expiryDuration)

		if err := sess.Save(); err != nil {
			s.logger.Errorw("failed to save session", "error", err)
			return fiber.NewError(http.StatusInternalServerError)
		}

		return c.JSON(fiber.Map{"user": user})
	})

	app.Get("/logout", func(c *fiber.Ctx) error {
		sess, err := s.sessionStore.Get(c)
		if err != nil {
			s.logger.Errorw("failed to get session", "error", err)
			return fiber.NewError(http.StatusInternalServerError)
		}

		if err := sess.Destroy(); err != nil {
			s.logger.Errorw("failed to destroy session", "error", err)
			return fiber.NewError(http.StatusInternalServerError)
		}

		redirect := c.Query("return", "/")
		return c.Redirect(redirect)
	})

	app.Get("/state", func(c *fiber.Ctx) error {
		states, err := s.queries.GetStates(c.Context())
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError, "failed to get states")
		}

		return c.Render("views/states", fiber.Map{"States": states})
	})

	app.Get("/state/:state", func(c *fiber.Ctx) error {
		state, err := url.QueryUnescape(c.Params("state"))
		if err != nil {
			return fiber.NewError(http.StatusBadRequest)
		}

		districts, err := s.queries.GetDistricts(c.Context(), pgtype.Text{String: state, Valid: true})
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError, "failed to get districts")
		}

		return c.Render("views/state", fiber.Map{"Districts": districts, "State": state})
	})

	app.Get("/state/:state/district/:district", func(c *fiber.Ctx) error {
		state, err := url.QueryUnescape(c.Params("state"))
		if err != nil {
			return fiber.NewError(http.StatusBadRequest)
		}
		district, err := url.QueryUnescape(c.Params("district"))
		if err != nil {
			return fiber.NewError(http.StatusBadRequest)
		}

		pincodes, err := s.queries.GetPincodeByDistrict(c.Context(), queries.GetPincodeByDistrictParams{
			District:  pgtype.Text{String: district, Valid: true},
			Statename: pgtype.Text{String: state, Valid: true},
		})
		if err != nil {
			return fiber.NewError(http.StatusInternalServerError, "failed to get pincodes")
		}

		return c.Render("views/district", fiber.Map{
			"State":    state,
			"District": district,
			"Pincodes": pincodes,
		})
	})
}
