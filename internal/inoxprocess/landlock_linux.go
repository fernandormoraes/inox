//go:build linux

package inoxprocess

import (
	"errors"
	"io/fs"
	"os/exec"
	"strings"

	"github.com/inoxlang/inox/internal/core"
	"github.com/inoxlang/inox/internal/globals/fs_ns"
	"github.com/inoxlang/inox/internal/permkind"
	"github.com/inoxlang/inox/internal/utils"
	"github.com/shoenig/go-landlock"
)

func restrictProcessAccess(grantedPerms, forbiddenPerms []core.Permission, fls *fs_ns.OsFilesystem, additionalPaths []*landlock.Path) {
	allowedPaths := []*landlock.Path{landlock.VMInfo(), landlock.Stdio(), landlock.Shared()}
	allowedPaths = append(allowedPaths, additionalPaths...)

	var allowDNS, allowCerts bool

	executablePaths := map[string]struct{}{}
	dirPaths := map[string]map[permkind.PermissionKind]struct{}{}
	filePaths := map[string]map[permkind.PermissionKind]struct{}{}

	for _, perm := range grantedPerms {
		switch p := perm.(type) {
		case core.DNSPermission:
			allowDNS = true
		case core.WebsocketPermission:
			allowCerts = true
		case core.HttpPermission:
			allowCerts = true
		case core.CommandPermission:
			var allowedPath *landlock.Path

			switch cmdName := p.CommandName.(type) {
			case core.Path:
				name := string(cmdName)
				if _, ok := executablePaths[name]; ok {
					continue
				}

				executablePaths[name] = struct{}{}
				allowedPath = landlock.File(name, "rx")
			case core.PathPattern:
				if cmdName.IsPrefixPattern() {
					allowedPath = landlock.Dir(cmdName.Prefix(), "rx")
				} else {
					panic(core.ErrUnreachable)
				}
			case core.Str:
				path, err := exec.LookPath(cmdName.UnderlyingString())
				if err != nil {
					panic(err)
				}
				if _, ok := executablePaths[path]; ok {
					continue
				}

				executablePaths[path] = struct{}{}
				allowedPath = landlock.File(path, "rx")
			default:
				panic(core.ErrUnreachable)
			}
			allowedPaths = append(allowedPaths, allowedPath)
		case core.FilesystemPermission:
			var allowedPathString string

			dir := true

			switch entity := p.Entity.(type) {
			case core.Path:
				allowedPathString = entity.UnderlyingString()

				if entity.IsDirPath() {
					dir = false
				}

			case core.PathPattern:
				if entity.IsPrefixPattern() {
					allowedPathString = entity.Prefix()
				} else {
					//we try to find the longest path that contains all matched paths.

					segments := strings.Split(entity.UnderlyingString(), "/")
					lastIncludedSegmentIndex := -1

					//search the rightmost segment that has no special chars.
				loop:
					for segmentIndex, segment := range segments {
						runes := []rune(segment)

						for i, r := range runes {
							switch r {
							case '*', '?', '[':
								//ignore if escaped
								if i > 0 && utils.CountPrevBackslashes(runes, int32(i))%2 == 1 {
									continue
								}
								lastIncludedSegmentIndex = segmentIndex
								break loop
							}
						}
					}

					if lastIncludedSegmentIndex >= 0 {
						dir := strings.Join(segments[:lastIncludedSegmentIndex+1], "/")
						allowedPathString = dir
					} else if entity.IsDirGlobbingPattern() {
						allowedPathString = entity.UnderlyingString()
					} else {
						dir = false
						allowedPathString = entity.UnderlyingString()
					}
				}
			default:
				panic(core.ErrUnreachable)
			}

			//ignore non existing paths
			if _, err := fls.Stat(allowedPathString); errors.Is(err, fs.ErrNotExist) {
				continue
			}

			if dir {
				map_, ok := dirPaths[allowedPathString]
				if !ok {
					map_ = map[permkind.PermissionKind]struct{}{}
					dirPaths[allowedPathString] = map_
				}

				map_[p.Kind_.Major()] = struct{}{}
			} else {
				map_, ok := filePaths[allowedPathString]
				if !ok {
					map_ = map[permkind.PermissionKind]struct{}{}
					filePaths[allowedPathString] = map_
				}

				map_[p.Kind_.Major()] = struct{}{}
			}
		}
	}

	getMode := func(kinds map[core.PermissionKind]struct{}) string {
		read := false
		write := false
		create := false

		for kind := range kinds {
			switch kind {
			case permkind.Read:
				read = true
			case permkind.Write:
				write = true
				create = true
			case permkind.Delete:
				write = true
			}
		}

		s := ""
		if read {
			s += "r"
		}
		if write {
			s += "w"
		}
		if create {
			s += "c"
		}
		return s
	}

	for path, kinds := range dirPaths {
		allowedPaths = append(allowedPaths, landlock.Dir(path, getMode(kinds)))
	}

	for path, kinds := range filePaths {
		allowedPaths = append(allowedPaths, landlock.File(path, getMode(kinds)))
	}

	if allowDNS {
		allowedPaths = append(allowedPaths, landlock.DNS())
	}

	if allowCerts {
		allowedPaths = append(allowedPaths, landlock.Certs())
	}

	locker := landlock.New(allowedPaths...)
	safety := landlock.OnlySupported //if running on Linux, require Landlock support.

	err := locker.Lock(safety)
	if err != nil {
		panic(err)
	}
}
