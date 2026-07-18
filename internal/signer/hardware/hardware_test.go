package hardware

import (
	"testing"

	"codeberg.org/pasiphae/callisto/internal/signer"
)

func TestDerivationPath(t *testing.T) {
	p := DerivationPath(5)
	if len(p) != 5 {
		t.Fatalf("path len = %d, want 5", len(p))
	}
	// m/44'/60'/0'/0/5
	want := []uint32{0x80000000 + 44, 0x80000000 + 60, 0x80000000 + 0, 0, 5}
	for i := range want {
		if p[i] != want[i] {
			t.Errorf("path[%d] = %d, want %d", i, p[i], want[i])
		}
	}
	// Deriving a different index must not mutate the shared base path.
	if DerivationPath(9)[4] != 9 || DerivationPath(3)[4] != 3 {
		t.Error("DerivationPath must return independent paths")
	}
}

func TestOpenLatticeUnsupported(t *testing.T) {
	if _, err := Open(signer.KindLattice, 0, ""); err != ErrLatticeUnsupported {
		t.Errorf("Lattice err = %v, want ErrLatticeUnsupported", err)
	}
}

func TestOpenUnsupportedKind(t *testing.T) {
	if _, err := Open(signer.KindHot, 0, ""); err != ErrUnsupportedKind {
		t.Errorf("hot-as-hardware err = %v, want ErrUnsupportedKind", err)
	}
}

func TestAccountsLatticeUnsupported(t *testing.T) {
	if _, err := Accounts(signer.KindLattice, 0, 3, ""); err != ErrLatticeUnsupported {
		t.Errorf("Accounts(Lattice) err = %v, want ErrLatticeUnsupported", err)
	}
}
