package spawn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// HerdrRequest is a structured launch, already rendered by the engine's owner.
// Choice belongs to the requester, not the daemon that handles the request.
type HerdrRequest struct {
	Choice BackendChoice
	Seat   fleetdb.Seat
	Run    fleetdb.SeatRun
	Paths  paths.Values
}

type HerdrResult struct {
	Seat fleetdb.Seat
	Eval string
	// Warnings names each running chat a new tab's placement could not move
	// into its place's workspace; the birth went on.
	Warnings []string
}

var nativeSeatID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// PrepareHerdr reserves a durable seat in the current bare pane for a launcher
// or restoration; the seat stays in the caller's pane and workspace, and
// places nothing.
func PrepareHerdr(ctx context.Context, request HerdrRequest) (result HerdrResult, returnErr error) {
	return reserveHerdr(ctx, request, request.Choice.HerdrPane)
}

func reserveHerdr(ctx context.Context, request HerdrRequest, pane string) (result HerdrResult, returnErr error) {
	if err := naming.ValidateLabel(request.Seat.Name); err != nil {
		return result, err
	}
	choice := request.Choice
	if choice.Backend != BackendHerdr || !filepath.IsAbs(choice.HerdrSocket) || choice.HerdrWorkspace == "" ||
		choice.HerdrPane == "" {
		return result, errors.New("native seat requires a verified Herdr backend, socket, pane and workspace")
	}
	descriptor, err := pfmengine.Lookup(pfmengine.ID(request.Seat.Engine))
	if err != nil {
		return result, err
	}
	if request.Run.Binary == "" || !filepath.IsAbs(request.Run.CWD) {
		return result, errors.New("native seat requires a binary and absolute cwd")
	}
	words := append(
		append([]string{request.Run.Binary, request.Run.CWD, request.Seat.Name}, request.Run.Args...),
		request.Run.Env...)
	for _, word := range words {
		if strings.ContainsRune(word, 0) {
			return result, errors.New("native seat values cannot contain NUL")
		}
	}
	var binary string
	if descriptor.ID == pfmengine.Claude {
		binary, err = gather.ResolveClaudeBinary(request.Paths.Home, request.Run.Binary, (paths.OSEnv{}).Get("PATH"))
	} else {
		binary, err = deps.Resolve(request.Run.Binary)
		if err == nil {
			binary, err = filepath.Abs(binary)
		}
	}
	if err != nil {
		return result, fmt.Errorf("resolve native %s engine: %w", descriptor.LongName, err)
	}
	request.Run.Binary = binary
	if err := herdr.CheckProtocol(ctx, choice.HerdrSocket); err != nil {
		return result, fmt.Errorf("native seat protocol: %w", err)
	}
	seat := request.Seat
	if seat.SeatID == "" {
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return result, fmt.Errorf("new native seat id: %w", err)
		}
		seat.SeatID = hex.EncodeToString(bytes[:])
	}
	if !nativeSeatID.MatchString(seat.SeatID) || seat.SessionID == "" {
		return result, errors.New("native seat requires a safe seat id and session id")
	}
	seat.Backend = BackendHerdr
	seat.HerdrSocket = choice.HerdrSocket
	seat.HerdrWorkspace = choice.HerdrWorkspace
	seat.HerdrPane = pane
	seat.State = "live"
	seat.UpdatedAt = clock.Real.Now().Unix()
	seat.TmuxSocket = ""
	seat.TmuxPane = ""
	state := fleetdb.OpenSharedState(ctx, request.Paths)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	if _, found, err := state.Seat(ctx, seat.SeatID); err != nil {
		return result, err
	} else if found {
		return result, fmt.Errorf("native seat %s already exists", seat.SeatID)
	}
	if err := state.WriteSeatRun(seat.SeatID, request.Run); err != nil {
		return result, err
	}
	if err := state.PutSeat(ctx, seat); err != nil {
		return result, errors.Join(err, state.DeleteSeatRun(seat.SeatID))
	}
	return HerdrResult{Seat: seat, Eval: "exec pfm internal seat-host " + seat.SeatID}, nil
}

// BirthTabLabel is the label a chat's tab is born with, never empty: the
// chat's name, else the base name of its working directory (the project
// folder an unnamed chat runs in), each as Herdr would show it.
func BirthTabLabel(name, cwd string) string {
	folder := filepath.Base(cwd)
	for _, label := range []string{name, folder} {
		if cleaned := herdr.TabLabel(label); cleaned != "" {
			return cleaned
		}
	}
	return folder
}

// SpawnHerdr creates one unfocused tab in the workspace of the chat's place
// (place.Fleet.Of its cwd), creating that workspace when the place has none,
// after moving every other misplaced running chat on the socket into its own.
func SpawnHerdr(ctx context.Context, request HerdrRequest) (result HerdrResult, returnErr error) {
	if request.Choice.Places == nil {
		return result, errors.New("herdr birth has no fleet places to file the chat under")
	}
	fleet, err := request.Choice.Places(ctx)
	if err != nil {
		return result, fmt.Errorf("place Herdr tab: %w", err)
	}
	result, err = reserveHerdr(ctx, request, "")
	if err != nil {
		return result, err
	}
	state := fleetdb.OpenSharedState(ctx, request.Paths)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	cleanup := func(cause error) error {
		return errors.Join(
			cause,
			state.DeleteSeat(context.WithoutCancel(ctx), result.Seat.SeatID),
			state.DeleteSeatRun(result.Seat.SeatID),
		)
	}
	workspace, unmoved, err := placeHerdrBirth(ctx, request, *fleet)
	if err != nil {
		return HerdrResult{}, cleanup(fmt.Errorf("place Herdr tab: %w", err))
	}
	if unmoved != nil {
		result.Warnings = append(result.Warnings, "Herdr places: "+unmoved.Error())
	}
	params := map[string]any{
		"workspace_id": workspace,
		"tab_label":    BirthTabLabel(request.Seat.Name, request.Run.CWD),
		"focus":        false,
		"root": map[string]any{
			"type":    "pane",
			"command": []string{"pfm", "internal", "seat-host", result.Seat.SeatID},
			"cwd":     request.Run.CWD,
		},
	}
	body, err := herdr.SeatRequest(ctx, request.Choice.HerdrSocket, "layout.apply", params)
	if err != nil {
		var refusal *herdr.Refusal
		if errors.As(err, &refusal) {
			return HerdrResult{}, cleanup(fmt.Errorf("spawn Herdr tab: %w", err))
		}
		return result, fmt.Errorf("herdr tab may exist for seat %s; reservation retained: %w", result.Seat.SeatID, err)
	}
	var reply struct {
		Layout struct {
			Workspace string `json:"workspace_id"`
			Root      struct {
				Type string `json:"type"`
				Pane string `json:"pane_id"`
			} `json:"root"`
		} `json:"layout"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return result, fmt.Errorf("herdr tab may exist for seat %s: decode layout: %w", result.Seat.SeatID, err)
	}
	if reply.Layout.Workspace != workspace || reply.Layout.Root.Type != "pane" ||
		reply.Layout.Root.Pane == "" {
		return result, fmt.Errorf(
			"herdr tab may exist for seat %s: unexpected layout response %s",
			result.Seat.SeatID,
			body,
		)
	}
	seat, found, err := state.Seat(ctx, result.Seat.SeatID)
	if err != nil {
		return result, fmt.Errorf("herdr pane %s exists; read reservation: %w", reply.Layout.Root.Pane, err)
	}
	if !found {
		return result, fmt.Errorf(
			"herdr pane %s exists but reservation %s is missing",
			reply.Layout.Root.Pane,
			result.Seat.SeatID,
		)
	}
	before := seat
	seat.HerdrPane, seat.HerdrWorkspace = reply.Layout.Root.Pane, workspace
	seat.UpdatedAt = clock.Real.Now().Unix()
	if err := state.UpdateSeat(ctx, before, seat); err != nil {
		return result, fmt.Errorf("herdr pane %s exists; persist address: %w", seat.HerdrPane, err)
	}
	result.Seat = seat
	return result, nil
}
