package region

import (
	"bytes"
	"compress/zlib"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/mj41/go-mc26/nbt"
)

func TestIn(t *testing.T) {
	for i := -10000; i < 10000; i++ {
		getX, _ := In(i, i)
		want := i % 32
		if want < 0 {
			want += 32
		}

		if getX != want {
			t.Errorf("fail when convert cord: get %d, want %d", getX, want)
		}
	}
}

// fixtureRegion is the region file cut from a vanilla server's world of this
// version (`mc26 fixtures`, ../testdata/world/SOURCE).
func fixtureRegion(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat("../testdata/world/SOURCE"); err != nil {
		t.Skip("no fixture world for this version yet: the end-to-end run writes it (`mc26 fixtures`)")
	}
	files, _ := filepath.Glob("../testdata/world/region/r.*.mca")
	if len(files) == 0 {
		t.Fatal("no fixture region under ../testdata/world")
	}
	return files[0]
}

func TestReadRegion(t *testing.T) {
	r, err := OpenReadOnly(fixtureRegion(t))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	for i := 0; i < 32; i++ {
		for j := 0; j < 32; j++ {
			s, err := r.ReadSector(In(i, j))
			if err != nil {
				continue
			}

			r, err := zlib.NewReader(bytes.NewReader(s[1:]))
			if err != nil {
				t.Error(err)
			}
			var b any
			_, err = nbt.NewDecoder(r).Decode(&b)
			if err != nil {
				t.Error(err)
			}
			// t.Log(b)
		}
	}
}

func TestFindSpace(t *testing.T) {
	r := &Region{
		sectors: map[int32]bool{
			0: true, 1: true,
			2: false, 3: true,
			4: false, 5: false, 6: true,
			7: false, 8: false, 9: false, 10: true, 11: true,
			12: false, 13: false, 14: false, 15: false, 16: false, 17: true,
			18: false, 19: false, 20: false, 21: false, 22: false, 23: false, 24: false,
		},
	}

	for _, test := range []struct{ need, index int32 }{
		{0, 0},
		{1, 2},
		{2, 4},
		{3, 7},
		{4, 12},
		{5, 12},
		{6, 18},
	} {
		i := r.findSpace(test.need)
		if i != test.index {
			t.Errorf("scan sctors fail: get %d, want %d", i, test.index)
		}
	}
}

func TestCountChunks(t *testing.T) {
	r, err := OpenReadOnly(fixtureRegion(t))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var count int
	for i := 0; i < 32; i++ {
		for j := 0; j < 32; j++ {
			if r.ExistSector(i, j) {
				count++
			}
		}
	}
	if count == 0 {
		t.Fatal("the fixture region holds no chunk")
	}
	t.Logf("chunk count: %d", count)
}

func TestWriteSectors(t *testing.T) {
	temp, err := os.CreateTemp("", "region*.mca")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := temp.Close(); err != nil {
			t.Error(err)
		}
		if err := os.Remove(temp.Name()); err != nil {
			t.Error(err)
		}
	}()
	region, err := CreateWriter(temp)
	if err != nil {
		t.Fatal(err)
	}

	expectedSectorsNum := 2
	for idx, test := range []struct{ size, sectors int }{
		{1, 1},
		{1000, 1},
		{4091, 1},
		{4092, 1},
		{4093, 2},
		{5000, 2},
	} {
		expectedSectorsNum += test.sectors

		data := make([]byte, test.size)
		rand.Read(data)
		if err = region.WriteSector(idx, 0, data); err != nil {
			t.Fatal("write sector", err)
		}
		if len(region.sectors) != expectedSectorsNum {
			t.Errorf("wrong region sector count. Got: %d, Want: %d", len(region.sectors), expectedSectorsNum)
		}

		if read, err := region.ReadSector(idx, 0); err != nil {
			t.Fatal("read sector", err)
		} else if !bytes.Equal(data, read) {
			t.Fatal("read corrupted sector data")
		}
	}

	// reset file
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}

	// Test load
	region, err = Load(temp)
	if err != nil {
		t.Fatalf("load region: %v", err)
	}
	if len(region.sectors) != expectedSectorsNum {
		t.Fatalf("read sector count missmatch. Got: %d, Want: %d", len(region.sectors), expectedSectorsNum)
	}

	// Test padding
	if err = region.PadToFullSector(); err != nil {
		t.Fatal(err)
	}
	if stat, err := temp.Stat(); err != nil {
		t.Fatal(err)
	} else if stat.Size()%4096 != 0 {
		t.Fatalf("wrong file size. Got %d, Want: %d", stat.Size(), stat.Size()+(4096-stat.Size()%4096))
	}
}
