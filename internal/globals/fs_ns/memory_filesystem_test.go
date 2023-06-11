package fs_ns

import (
	"testing"

	billy "github.com/go-git/go-billy/v5"
	"gopkg.in/check.v1"

	"github.com/stretchr/testify/assert"
)

var _ = check.Suite(&MemoryFsTestSuite{})

type MemoryFsTestSuite struct {
	BasicTestSuite
	DirTestSuite
}

func (s *MemoryFsTestSuite) SetUpTest(c *check.C) {
	s.BasicTestSuite = BasicTestSuite{
		FS: NewMemFilesystem(100_000_00),
	}
	s.DirTestSuite = DirTestSuite{
		FS: NewMemFilesystem(100_000_00),
	}
}

func TestMemoryFilesystem(t *testing.T) {
	check.TestingT(t)
}

func TestMemoryFilesystemCapabilities(t *testing.T) {
	cases := []struct {
		name     string
		caps     billy.Capability
		expected bool
	}{
		{
			name:     "not lock capable",
			caps:     billy.LockCapability,
			expected: false,
		},
		{
			name:     "read capable",
			caps:     billy.ReadCapability,
			expected: true,
		},
		{
			name:     "read+write capable",
			caps:     billy.ReadCapability | billy.WriteCapability,
			expected: true,
		},
		{
			name:     "read+write+truncate capable",
			caps:     billy.ReadCapability | billy.WriteCapability | billy.ReadAndWriteCapability | billy.TruncateCapability,
			expected: true,
		},
		{
			name:     "not read+write+truncate+lock capable",
			caps:     billy.ReadCapability | billy.WriteCapability | billy.ReadAndWriteCapability | billy.TruncateCapability | billy.LockCapability,
			expected: false,
		},
		{
			name:     "not truncate+lock capable",
			caps:     billy.TruncateCapability | billy.LockCapability,
			expected: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fs := NewMemFilesystem(10_000_000)
			assert.Equal(t, testCase.expected, billy.CapabilityCheck(fs, testCase.caps))
		})
	}
}
