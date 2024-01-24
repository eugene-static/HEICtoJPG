package main

import (
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/jdeng/goheif"
)

const (
	heic = "HEIC"
	jpg  = "JPG"
	sep  = "."
)

type writeSkipper struct {
	w           io.Writer
	bytesToSkip int
}

func main() {
	// App Window
	htj := app.New()
	window := htj.NewWindow("HEICtoJPG")
	window.SetCloseIntercept(func() {
		htj.Quit()
	})
	windowSize := fyne.Size{Width: 500, Height: 200}
	window.Resize(windowSize)
	var (
		counter     int
		path        string
		toDelete    bool
		startButton *widget.Button
	)
	window.SetFixedSize(true)
	//favicon, err := os.ReadFile("./assets/Icon.png")
	//if err != nil {
	//	log.Println("failed to open icon file")
	//}
	//window.SetIcon(fyne.NewStaticResource("favicon", favicon))
	// Entry
	pathEntry := widget.NewEntry()
	pathEntry.PlaceHolder = "Folder path"
	pathEntry.OnChanged = func(s string) {
		if s != "" {
			path = s
			startButton.Enable()
		} else {
			startButton.Disable()
		}
	}

	// File Dialog
	fileDialogWindow := htj.NewWindow("Choose folder")
	fileDialogWindow.Resize(fyne.Size{
		Width:  500,
		Height: 500,
	})
	fileDialogWindow.SetCloseIntercept(func() {
		fileDialogWindow.Hide()
	})
	fileDialog := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if uri != nil {
			path = uri.Path()
			pathEntry.SetText(uri.Path())
		}
		fileDialogWindow.Hide()
	}, fileDialogWindow)

	// Open Folder Button
	choosePathButton := widget.NewButtonWithIcon("", theme.FolderIcon(), func() {
		fileDialogWindow.Show()
		fileDialog.Show()
		fileDialog.Resize(fyne.Size{
			Width:  500,
			Height: 500,
		})
	})

	// Output
	output := widget.NewTextGrid()
	outputCont := container.NewScroll(output)

	// Check
	check := widget.NewCheck("Delete originals", func(b bool) {
		toDelete = b
	})

	// Start, Cancel Button
	stop := make(chan struct{})
	cancelButton := widget.NewButton("Cancel", func() {
		stop <- struct{}{}
	})
	cancelButton.Disable()
	startButton = widget.NewButton("Convert", func() {
		msg := make(chan string)
		startButton.Disable()
		cancelButton.Enable()
		choosePathButton.Disable()
		pathEntry.Disable()
		check.Disable()
		output.SetText("")
		go func() {
			var err error
			counter, err = decode(path, msg, stop, toDelete)
			if err != nil {
				addText(output, err.Error()+"\n")
				close(msg)
				return
			}
		}()
		go func() {
			for m := range msg {
				addText(output, m)
				outputCont.ScrollToBottom()
			}
			if counter == 0 {
				addText(output, "Files not found")
			} else {
				addText(output, fmt.Sprintf("Files converted: %d", counter))
			}
			outputCont.ScrollToBottom()
			startButton.Enable()
			cancelButton.Disable()
			check.Enable()
			choosePathButton.Enable()
			pathEntry.Enable()
		}()
	})
	startButton.Disable()

	// Constructor
	folderRow := container.New(layout.NewFormLayout(), choosePathButton, pathEntry)
	startRow := container.NewVBox(check, container.NewGridWithColumns(2, startButton, cancelButton))
	window.SetContent(container.NewBorder(folderRow, startRow, nil, nil, outputCont))
	window.ShowAndRun()
}

func addText(textGrid *widget.TextGrid, text string) {
	text = textGrid.Text() + text
	textGrid.SetText(text)
}

func decode(root string, msg chan string, stop chan struct{}, toDelete bool) (int, error) {
	counter := 0
	err := filepath.Walk(root, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-stop:
			return errors.New("Stopped\n")
		default:
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()[0:strings.LastIndex(info.Name(), sep)]
		ext := strings.ToUpper(filepath.Ext(info.Name()))
		if ext == sep+heic {
			failed := false
			msg <- fmt.Sprintf("%s...", info.Name())
			file, err := os.Open(path)
			if err != nil {
				msg <- fmt.Sprintf("failed to open %s: %v\n", info.Name(), err)
				failed = true
			}
			exif, err := goheif.ExtractExif(file)
			if err != nil {
				msg <- fmt.Sprintf("warning: no EXIF from %s: %v\n", info.Name(), err)
				failed = true
			}
			img, err := goheif.Decode(file)
			if err != nil {
				msg <- fmt.Sprintf("failed to decode file %s: %v\n", info.Name(), err)
				failed = true
			}
			file.Close()
			dir := filepath.Dir(path)
			newFile, err := os.OpenFile(filepath.Join(dir, name+sep+jpg), os.O_RDWR|os.O_CREATE, 0644)
			if err != nil {
				msg <- fmt.Sprintf("failed to create file %s: %v\n", name+sep+jpg, err)
				failed = true
			}
			defer newFile.Close()
			w, err := newWriterExif(newFile, exif)
			if err != nil {
				msg <- fmt.Sprintf("failed to write EXIF into file %s: %v\n", name+sep+jpg, err)
				failed = true
			}
			err = jpeg.Encode(w, img, nil)
			if err != nil {
				msg <- fmt.Sprintf("failed to write file %s: %v\n", name+sep+jpg, err)
				failed = true
			}
			if !failed {
				counter++
				msg <- "OK\n"
				if toDelete {
					err = os.Remove(path)
					if err != nil {
						msg <- fmt.Sprintf("Warning: can't delete %s: %v\n", name+sep+heic, err)
					}
				}
			}
			failed = false
		}
		return nil
	})
	if err != nil {
		return counter, err
	}
	close(msg)
	return counter, nil
}

func (w *writeSkipper) Write(p []byte) (int, error) {
	if w.bytesToSkip <= 0 {
		return w.w.Write(p)
	}
	if pLen := len(p); pLen < w.bytesToSkip {
		w.bytesToSkip -= pLen
		return pLen, nil
	}
	if n, err := w.w.Write(p[w.bytesToSkip:]); err == nil {
		n += w.bytesToSkip
		w.bytesToSkip = 0
		return n, nil
	} else {
		return n, err
	}

}

func newWriterExif(w io.Writer, exif []byte) (io.Writer, error) {
	writer := &writeSkipper{w, 2}
	soi := []byte{0xff, 0xd8}
	if _, err := w.Write(soi); err != nil {
		return nil, err
	}
	if exif != nil {
		app1marker := 0xe1
		markerLen := 2 + len(exif)
		marker := []byte{0xff, uint8(app1marker), uint8(markerLen >> 8), uint8(markerLen & 0xff)}
		if _, err := w.Write(marker); err != nil {
			return nil, err
		}
		if _, err := w.Write(exif); err != nil {
			return nil, err
		}
	}
	return writer, nil
}
