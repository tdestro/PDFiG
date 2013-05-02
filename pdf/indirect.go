package pdf

import (
	"errors"
	"fmt"
	"strconv" )


// Implements:
// 	pdf.Object
//	bufio.Writer
type Indirect struct {
	fileBindings map[File]ObjectNumber
	// When not nil, sourceFile is a file this indirect object was read from.
	sourceFile      File
}

/*
pdf.Indirect is one of the most important and most complex
object types.  Whereas "direct" objects are rendered the same way on
every output stream, a pdf.Indirect is rendered differently
depending on which file it is associated with.  There is always an
underlying "direct object" that is written to one or more PDF files,
where it is assigned both an object and generation number.  When the
Serialize() method is invoked, a pdf.Indirect is rendered as an
indirect reference (for example, "10 1 R").  There are several
use-cases.

USE-CASE 1: A pdf.Indirect object is created, its Serialize() method
is invoked for one or more output files, then its Write() method is
invoked with its underlying direct object.  The PDF reference suggests
that a reference is sometimes written before the information required
for the object being referenced is available.  An example from the PDF
reference is using an indirect object reference for the length of the
stream before writing the stream, presumably so you can write the
stream dictionary at a moment when the length of the stream is
unknown.  The stream length is written as a separate object after the
stream is completed when the length is known with certainty.  This
particular example is somewhat contrived.  With the size of memory in
modern computers it's difficult to imagine a scenario where a stream
cannot be written to a buffer in its entirely and written as an atomic
stream object.  Nonetheless, a pdf.Indirect object supports this
programming style where object references are written to a file before
the objects they refer to have been completely defined.  We do *not*
support this model with streams, however, and *do* require streams to
be completed in memory before any portion of the strean is written to
a file.  A pdf.Indirect can be written before the direct object it
references has been defined.  A pdf.Indirect obtains and reserves an
object number whenever it is written to a file, whether or not the
object being referenced has yet been specified.  Eventually, the
Write() method must be called passing the object being referenced.
At that moment, the object being referenced is written to all files to
which the pdf.Indirect was written.  If the pdf.Indirect is
subsequently added to additional files, the Write()ed object must
also written to those files.  This, however, would require either
retaining a reference in memory indefinitely (bad) or reading it from
one of the files where it is known to exist (reading is not yet
implemented; it must be read as opposed to just copied because any
indirect references contained within it also need to be read and added
to the file accordingly).  For the time being, we elect not to keep
references in memory.  Until parsing is implemented, indirect objects
may be explicitly bound to files either (1) by using ObjectNumber()
prior to calling Write or (2) by calling pdf.File.AddObject().
Serialize()ing a pdf.Indirect to a new file after calling Write()
without an earlier call to Serialize() or ObjectNumber() will
generate an error.  The complete list of files that will contain the
refence must be known when Write() is called.

The call to ObjectNumber() is handled transparently and automatically
for forward references.  The client need not call it explicitly.  A
call to ObjectNumber() is required, however, for indirect objects that
are backward references.  An alternative way to obtain a backward
reference is using the return value from pdf.File.AddObject().  The
reference returned by pdf.File.AddObject() is bound only to one file.

USE-CASE 2: A pdf.Indirect is created based on a finished direct
object.  This is essentially the same as use-case 1.  An object is
constructed and Write() is called immediately.  Subsequent invocations
of Serialize() on files where it doesn't already exist cause it to be
added to that file.  As with USE-CASE 1, this requires either
retaining a reference in memory indefinitely (bad) or reading from one
of the files where it is known to exist (not yet implemented).  For
the time being, we elect not to keep references in memory. Until
parsing is implemented, so Serialize()ing a pdf.Indirect to a file
after calling Write() is an error.

USE-CASE 3: A pdf.Indirect is constructed when a token of the form "10
1 R" is read from file X.  The underlying direct object is unknown at
that moment, but it exists in X.  Since the object exists statically
in X, it is considered to have been finalized.  If the same object is
Serialize()'ed to file Y, then it should immediately be added to file
Y.  The objects contents are obtained from X.

POSSIBLE TEMPORARY BEHAVIOR: The referenced object is retained by the
pdf.Indirect so that it can be "dereferenced" or written to additional
files.  In future versions that are able to parse PDF files, the
object may be discarded from memory once written and read back from
disk if it is dererenced or written to another file.  Even better
would be a weak-reference to the object, but weak references are not
implemented in Go.

*/

// NewIndirect is the constructor for Indirect objects.  For
// convenience, if the files to which this indirect object should be
// bound are known at construction time, they may be provided as
// optional arguments.  Instead of providing these at construction,
// the client may call Indirect.ObjectNumber() after construction.
func NewIndirect(file... File) *Indirect {
	result := new(Indirect)
	result.fileBindings = make(map[File]ObjectNumber,1)
	result.sourceFile = nil

	for _,f := range file {
		result.ObjectNumber(f)
	}

	return result
}

func newIndirectWithNumber(objectNumber ObjectNumber, file File) *Indirect {
	result := new(Indirect)
	result.fileBindings = make(map[File]ObjectNumber,5)
	result.sourceFile = file
	result.fileBindings[file] = objectNumber
	return result
}

func (i *Indirect) Clone() Object {
	newIndirect := new(Indirect)
	newIndirect.fileBindings = i.fileBindings
	newIndirect.sourceFile = i.sourceFile
	return newIndirect
}

func (i *Indirect) Dereference() Object {
	if i.sourceFile == nil {
		panic (errors.New(`Attempt to deference an object with no known source`))
	}

	object,err := i.sourceFile.Object(i.ObjectNumber(i.sourceFile))
	if err != nil {
		panic (errors.New(fmt.Sprintf(`Unable to read object at %v`, i.ObjectNumber(i.sourceFile))))
	}
	// TODO:  Think about whether Dereference() is a good idea here.
	return object.Dereference()
}

// Serialize() writes a serial representation (as defined by the PDF
// specification) of the object to the Writer.  Indirect references
// are resolved and numbered as if they were being written to the
// optional File argument.  Having separate arguments for Writer and
// File allows writing an object to stdout, but using the indirect
// reference object numbers as if it were contained in a specific PDF
// file.
func (i *Indirect) Serialize(w Writer, file ...File) {
	if len(file) != 1 {
		panic("A single file parameter is required for pdf.Indirect.Serialize()")
	}
	if file[0].Closed() {
		panic("Attempt to Serialize to a closed file")
	}

	objectNumber := i.ObjectNumber(file[0])
	w.WriteString(strconv.FormatInt(int64(objectNumber.number), 10))
	w.WriteByte(' ')
	w.WriteString(strconv.FormatInt(int64(objectNumber.generation), 10))
	w.WriteString(" R")
}

// Write() writes the passed object as an indirect object (complete
// with an entry in the xref, an "obj" header, and an "endobj"
// trailer) to all files to which the Indirect object has been bound.
// Write() may be used to replace an existing object.
// Write() returns its Indirect object for constructions such as
//  a := NewIndirect(f).Write(object)
func (i *Indirect) Write(o Object) *Indirect{
	for file, objectNumber := range i.fileBindings {
		file.WriteObjectAt(objectNumber, o)
		if i.sourceFile == nil {
			i.sourceFile = file
		}
	}
	return i
}

// ObjectNumber() binds its object to the passed pdf.File object and
// returns an object number associated with that file.  Normally it is
// called automatically and transparently whenever an indirect
// reference is contained within some other object that is explicitly
// written to a file.  Client code may call ObjectNumber() explicitly
// if the client intends to write the object to a number of files
// using Indirect.Write().  that case, the caller should call
// ObjectNumber() one or more times *before* calling Write().
// Alternatively, client code may call File.Write(), which returns a
// *Indirect that may be used for backward references.  In the latter
// case, the reference will only be tied to only one file.
func (i *Indirect) ObjectNumber(f File) ObjectNumber {
	destObjectNumber,exists := i.fileBindings[f]
	if !exists {
		destObjectNumber = f.ReserveObjectNumber(i)
		i.fileBindings[f] = destObjectNumber
		// If the object was a pre-existing object, silently
		// add it to "file."
		if i.sourceFile != nil {
			sourceObjectNumber := i.fileBindings[i.sourceFile]
			o, err := i.sourceFile.Object(sourceObjectNumber)
			if err == nil {
				f.WriteObjectAt(destObjectNumber,o)
			}
		}
	}
	return destObjectNumber
}

func (i *Indirect) BoundToFile(f File) bool {
	_,exists := i.fileBindings[f]
	return exists
}
