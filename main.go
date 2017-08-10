package main

import (
    "fmt"
    "os"
    "bufio"
    "encoding/xml"
    "errors"
    "path/filepath"
    "strings"
    "github.com/fsnotify/fsnotify"
    "time"
    "os/signal"
    "syscall"
)

const humanDir   string = "NiceObjects"
const projectDir string = "example.gmx"
var gmObjectsDir = filepath.Join(projectDir, "objects")

const reverbSpacing time.Duration = 1 * time.Second
var gmChanged       time.Time
var humanChanged    time.Time

func main () {
    InitTranslations()

    // Start listening for SIGINT (Ctrl-C)

    sigchan := make(chan os.Signal, 2)
    signal.Notify(sigchan, os.Interrupt, syscall.SIGTERM)

    // Create human folder

    fmt.Println("Initializing NiceObjects directory...")

    err := os.MkdirAll(humanDir, os.ModePerm)
    if err != nil {
        fmt.Printf("Error creating NiceObjects directory: %v\n", err)
        return
    }

    // Translate all objects into human folder

    err = filepath.Walk(gmObjectsDir, initialTranslateWalkFunc)
    if err != nil {
        fmt.Printf("Error during initial translation of all GM objects "+
                "to human objects: %v\n", err)
        return
    }

    // Start monitoring files for changes

    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        fmt.Printf("Error creating fsnotify watcher: %v\n", err)
        return
    }
    defer watcher.Close()

    // Watcher must be in a separate goroutine
    go func () {
        for {
            select {
            case event := <-watcher.Events:
                processWatcherEvent(event)
            case err := <-watcher.Errors:
                fmt.Printf("Fsnotify Watcher error: %v\n", err)
            }
        }
    }()

    if err := watcher.Add(humanDir); err != nil {
        fmt.Printf("Error assigning human dir to fsnotify watcher: %v\n", err)
    }
    if err := watcher.Add(gmObjectsDir); err != nil {
        fmt.Printf("Error assigning GM objects dir to fsnotify watcher: %v\n",
                err)
    }

    // Surrender this goroutine to the watcher, and wait for control signal

    fmt.Println("Listening")

    <-sigchan

    // Remove created directory

    fmt.Println("Signal received, removing NiceObjects directory...")
    err = os.RemoveAll(humanDir)
    if err != nil {
        fmt.Printf("Error removing NiceObjects directory: %v\n", err)
        return
    }
    fmt.Println("Success")
}

func processWatcherEvent (event fsnotify.Event) {
    ext := filepath.Ext(event.Name)
    isHumanObj := strings.HasPrefix(event.Name, humanDir) &&
            ext == ".ogml"
    isGMObj := strings.HasPrefix(event.Name, gmObjectsDir) &&
            ext == ".gmx" // close enough to ".object.gmx"
    if isHumanObj {
        if event.Op == fsnotify.Write {
            if time.Since(gmChanged) > reverbSpacing {
                translateHumanObject(event.Name)
                humanChanged = time.Now()
            }
        }
    } else if isGMObj {
        if event.Op == fsnotify.Write {
            if time.Since(humanChanged) > reverbSpacing {
                translateGMObject(event.Name)
                gmChanged = time.Now()
            }
        }
    }
}

func translateHumanObject (humanObjPath string) {
    objName := strings.Split(filepath.Base(humanObjPath), ".")[0]
    gmObjPath := filepath.Join(gmObjectsDir, objName + ".object.gmx")

    err := HumanObjectFileToGMObjectFile(humanObjPath, gmObjPath)
    if err != nil {
        fmt.Printf("[%v] %v\n", time.Now().Format("15:04:05"), err)
    } else {
        fmt.Printf("[%v] Translated %v\n",
                time.Now().Format("15:04:05"), objName)
    }
}

func translateGMObject (gmObjPath string) {
    objName := strings.Split(filepath.Base(gmObjPath), ".")[0]
    humanObjPath := filepath.Join(humanDir, objName + ".ogml")

    err := GMObjectFileToHumanObjectFile(gmObjPath, humanObjPath)
    if err != nil {
        fmt.Printf("[%v] (From GM) %v\n", time.Now().Format("15:04:05"), err)
    } else {
        fmt.Printf("[%v] (From GM) Translated %v\n",
                time.Now().Format("15:04:05"), objName)
    }
}

func initialTranslateWalkFunc (gmObjPath string,
        info os.FileInfo, err error) error {
    if info.IsDir() {
        return nil
    }
    dotSep := strings.SplitN(filepath.Base(gmObjPath), ".", 2)
    if !(len(dotSep) == 2 && dotSep[1] == "object.gmx") {
        return nil
    }
    objName := dotSep[0] + ".ogml"
    humanObjPath := filepath.Join(humanDir, objName)
    return GMObjectFileToHumanObjectFile(gmObjPath, humanObjPath)
}

func GMObjectFileToHumanObjectFile (objFilename string,
        humanFilename string) error {
    // Read GM object file

    f, err := os.Open(objFilename)

    if err != nil {
        return errors.New(fmt.Sprintf("Error opening file %v: %v",
                objFilename, err))
    }

    defer f.Close()
    decoder := xml.NewDecoder(f)
    var obj GMObject
    err = decoder.Decode(&obj)

    if err != nil {
        return errors.New(fmt.Sprintf("Error decoding XML from %v: %v",
                objFilename, err))
    }

    // Write human object file

    f, err = os.Create(humanFilename)

    if err != nil {
        return errors.New(fmt.Sprintf("Error creating file %v: %v",
                humanFilename, err))
    }

    defer f.Close()
    w := bufio.NewWriter(f)
    err = WriteHumanObject(obj, w)

    if err != nil {
        return errors.New(fmt.Sprintf("Error writing human object to %v: %v",
                humanFilename, err))
    }

    w.Flush()
    return nil
}

func HumanObjectFileToGMObjectFile (humanFilename string,
        objFilename string) error {
    // Read GM object file

    f, err := os.Open(objFilename)

    if err != nil {
        return errors.New(fmt.Sprintf("Error opening file %v: %v",
                objFilename, err))
    }

    defer f.Close()
    decoder := xml.NewDecoder(f)
    var obj GMObject
    err = decoder.Decode(&obj)

    if err != nil {
        return errors.New(fmt.Sprintf("Error decoding XML from %v: %v",
                objFilename, err))
    }

    // Read human object file

    f, err = os.Open(humanFilename)
    if err != nil {
        return errors.New(fmt.Sprintf("Error opening file %v: %v",
                humanFilename, err))
    }
    defer f.Close()

    r := bufio.NewReader(f)
    err = ReadHumanObject(r, &obj)
    if err != nil {
        return errors.New(fmt.Sprintf("Error reading human object file %v: %v",
                humanFilename, err))
    }

    // Write GM object file

    f, err = os.Create(objFilename)
    if err != nil {
        return errors.New(fmt.Sprintf("Error opening file %v: %v",
                objFilename, err))
    }
    defer f.Close()

    encoder := xml.NewEncoder(f)
    encoder.Indent("", "  ")
    err = encoder.Encode(obj)
    if err != nil {
        return errors.New(fmt.Sprintf("Error writing XML to %v: %v",
                objFilename, err))
    }

    return nil
}
