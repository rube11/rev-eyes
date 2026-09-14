#ifndef MOONSHINE_C_API_H
#define MOONSHINE_C_API_H

/* Moonshine is a library for building interactive voice applications. It
   provides a high-level API for building voice interfaces, including
   voice-activity detection, diarization, transcription, speech understanding,
   and text-to-speech. It is designed to be fast, easy to use and to provide a
   high level of accuracy. It is also designed to be easy to integrate into your
   existing codebase across all major platforms.

   It uses the Moonshine family of speech to text models, which:

     - Understand multiple major languages, including English, Japanese,
       Korean, Chinese, Arabic, and more.

     - Are designed to be lightweight and fast for mobile and edge devices,
       and can be used in the cloud where latency and compute costs matter.

     - Support streaming transcription to reduce latency on real-time
       applications.

     - Are trained from scratch on a large, unique dataset of audio data,
       allowing our team to quickly train custom models for jargon or dialects.

     - Are available under permissive licenses, with English fully MIT
       licensed and other languages under a non-commercial agreement.

   You'll most likely want to use the specific bindings for your language of
   choice, since this is a low-level C API to the underlying implementation.
   This is the interface that those bindings all use though, so if you're
   interested in porting to a new environment or language, the inline notes
   here may be useful.

   Here's an example of how to use the transcriber:
   ```c
   #include "moonshine-c-api.h"

   int main(int argc, char *argv[]) {
     int32_t transcriber_handle = moonshine_load_transcriber_from_files(
       "path/to/models", MOONSHINE_MODEL_ARCH_BASE, NULL, 0,
       MOONSHINE_HEADER_VERSION);
     if (transcriber_handle < 0) {
       fprintf(stderr, "Failed to load transcriber\n");
       return 1;
     }

     float audio_data[32000] = {};
     size_t audio_length = 32000;
     int32_t sample_rate = 16000;
     transcript_t *transcript = NULL;
     int32_t error = moonshine_transcribe_without_streaming(transcriber_handle,
   audio_data, audio_length, sample_rate, 0, &transcript); if (error != 0) {
       fprintf(stderr, "Failed to transcribe\n");
       return 1;
     }
     for (size_t i = 0; i < transcript->line_count; i++) {
       printf( "Line %zu at %f seconds: %s\n", i, transcript->lines[i].start,
         transcript->lines[i].text);
     }
     moonshine_free_transcriber(transcriber_handle);
     return 0;
   }

   All API calls are thread-safe, so you can call them from multiple threads
   concurrently. Calculations on a single transcriber will be serialized
   however, so latency will be affected for calls from other threads while
   the transcriber is busy.
   ```
*/

#if defined(ANDROID)
#include <android/asset_manager.h>
#endif
#include <stddef.h>
#include <stdint.h>

#ifdef _WIN32
#define MOONSHINE_EXPORT __declspec(dllexport)
#else
#define MOONSHINE_EXPORT __attribute__((visibility("default")))
#endif

#ifdef __cplusplus
extern "C" {
#endif

/* ------------------------------ CONSTANTS -------------------------------- */

/* What version of the Moonshine library the header file is associated with.
   You should pass this version to moonshine_load_transcriber so that newer
   versions of the library can emulate any older behavior that has changed.
   The format is MAJOR * 10000 + MINOR * 100 + PATCH.
   For example, version 3.0.0 would be 30000.
   For example, version 3.2.7 would be 30207.                                */
#define MOONSHINE_HEADER_VERSION (30000)

/* The first header version that no longer supports
   moonshine_load_transcriber_from_memory. A client passing this version or
   newer gets MOONSHINE_ERROR_INVALID_ARGUMENT back from that call, along with
   a logged explanation, and should use
   moonshine_load_transcriber_from_memory_files instead. Clients built against
   an earlier header keep the old behavior, so existing binaries are
   unaffected.                                                               */
#define MOONSHINE_FROM_MEMORY_REMOVED_VERSION (30000)

/* Supported model architectures.                                            */
#define MOONSHINE_MODEL_ARCH_TINY (0)
#define MOONSHINE_MODEL_ARCH_BASE (1)
#define MOONSHINE_MODEL_ARCH_TINY_STREAMING (2)
/* Note: BASE_STREAMING is defined for future use but is not currently
   published in the model catalog.                                           */
#define MOONSHINE_MODEL_ARCH_BASE_STREAMING (3)
#define MOONSHINE_MODEL_ARCH_SMALL_STREAMING (4)
#define MOONSHINE_MODEL_ARCH_MEDIUM_STREAMING (5)

/* Error codes.                                                            */
#define MOONSHINE_ERROR_NONE (0)
#define MOONSHINE_ERROR_UNKNOWN (-1)
#define MOONSHINE_ERROR_INVALID_HANDLE (-2)
#define MOONSHINE_ERROR_INVALID_ARGUMENT (-3)
/* A streaming generation is in flight and the call would have competed with it
   for the model. Finish it, or call moonshine_tts_cancel.                   */
#define MOONSHINE_ERROR_BUSY (-4)

/* Statuses from moonshine_tts_next_chunk. All are positive, so the usual
   "negative means failure" test still separates them from real errors, and
   all convert with moonshine_error_to_string.                              */
/* No complete sentence is buffered yet. Push more text, or flush.          */
#define MOONSHINE_TTS_NEED_TEXT (1)
/* Input ended and every queued utterance has been synthesized.             */
#define MOONSHINE_TTS_END_OF_STREAM (2)
/* moonshine_tts_cancel discarded the reply that was being generated. Sent
   once, and only when there was something to discard, so a consumer pulling
   chunks on a worker thread can tell an interruption from running out of
   text.                                                                    */
#define MOONSHINE_TTS_CANCELLED (3)

/* Flags.                                                                */
#define MOONSHINE_FLAG_FORCE_UPDATE (1 << 0)
/* Apply alphanumeric-spelling fusion to every completed line in the
   returned transcript. The transcriber must have been constructed with
   a spelling model (either ``spelling_model_path`` in
   moonshine_load_transcriber_from_files or a non-null
   ``spelling_model_data`` buffer in
   moonshine_load_transcriber_from_memory) for this flag to have any
   effect; if no spelling model is loaded, the flag is ignored.

   When fusion fires for a line, the line's ``text`` field is *replaced*
   with the resolved single character (e.g. ``"a"`` or ``"$"``). Speech
   that does not resolve to a character is left unchanged so command
   words like "stop" / "clear" / "delete" can still be classified by
   higher-level Python code. */
#define MOONSHINE_FLAG_SPELLING_MODE (1 << 1)

/* --------------------------- DATA STRUCTURES ----------------------------- */

/* Values passed to moonshine_load_transcriber,
   moonshine_create_text_to_speech_synthesizer or
   moonshine_create_graph_to_phonemizer at creation time that control
   the behavior of the transcriber. A typical use case would be to specify
   model configuration options like layer names that vary by language. The
   value is a string. You don't normally need to care about these, this is just
   for advanced customizations.                                              */
struct moonshine_option_t {
  const char *name;
  const char *value;
};

/* All transcription calls return a list of "lines". These line objects
represent a piece of speech, something like a sentence or phrase. For
non-streaming calls, you get back a finalized list of these lines, with all
their states set to “complete”. Each streaming call returns a similar list, but
if there isn’t a pause at the end of the current audio - if the user still
seems to be speaking but cut off - the final line will be marked as being
incomplete.

All memory referenced by the line objects is owned by the transcriber and is
valid until the next call to that transcriber, or until the transcriber is
freed.

The audio data is 16KHz float PCM, between -1.0 and 1.0.

To make the streaming results easier to work with we offer some guarantees:

 - Lines are never removed from the results, only added.

 - Only the last line in the list may potentially be incomplete.

 - If speech is detected by the VAD, but no transcription can be produced, the
   line will be an empty string, "".

 - Line indexes can be used as stable references when repeatedly calling
   streaming transcription. This means a client can remember the length of the
   last results returned, and when it calls again it can figure out the updates
   by iterating the results starting at that line index.

 - The line id is a stable identifier for the line. This is set to a 64-bit
   randomly-generated number, with the goal of minimizing the chances of a
   collision. Currently these IDs are in ascending order in any one transcript,
   but this is not guaranteed and should not be relied on.

 - When speaker identification is enabled (the opt-in ``identify_speakers``
   option), each line carries an array of speaker spans describing who was
   talking during which parts of the line, including UTF-8 character ranges
   into the line text. Word timestamps are enabled automatically in this mode.
   Speaker IDs are 64-bit
   randomly-generated numbers that are stable for a given speaker within a
   stream, and speaker indices count speakers in order of first appearance.
   Unlike the text and timing of a line, speaker spans for recent audio are
   *mutable*: streaming diarization re-clusters a sliding window
   (``diarization_cluster_window_sec``, default 120s) as more speech arrives;
   assignments for older audio are frozen. The ``have_speakers_changed`` flag is
   set on a line whenever its spans changed since the previous call.

See the stream transcription examples below for more details on what this
means in practice.
*/

/* A single word with timing information.
   Only populated when word_timestamps option is enabled. */
struct transcript_word_t {
  /* UTF-8-encoded word text. */
  const char *text;
  /* Start time in seconds (absolute, from start of audio/stream). */
  float start;
  /* End time in seconds. */
  float end;
  /* Model confidence score, 0.0 to 1.0. */
  float confidence;
};

/* One contiguous span of speech within a line attributed to a single
   speaker. Only populated when the identify_speakers option is enabled.
   Spans can be revised on any transcription call, even for lines that are
   already complete; see the have_speakers_changed flag on
   transcript_line_t. Character ranges use UTF-8 byte offsets into the line
   text; word_timestamps are enabled automatically when identify_speakers is
   on. */
struct speaker_span_t {
  /* Time offset from the start of the array or stream in seconds. */
  float start_time;
  /* Length of the span in seconds. */
  float duration;
  /* Stable identifier for the speaker within this stream. */
  uint64_t speaker_id;
  /* The order the speaker first appeared in the transcript, starting at 0. */
  uint32_t speaker_index;
  /* UTF-8 byte offset into the line's text where this span begins (inclusive).
     Only meaningful when identify_speakers is enabled; word_timestamps are
     turned on automatically in that case. Both zero when unknown. */
  uint64_t start_char;
  /* UTF-8 byte offset into the line's text where this span ends (exclusive).
     Both zero when unknown. */
  uint64_t end_char;
};

/* Information about a single “line” of a transcript. */
struct transcript_line_t {
  /* UTF-8-encoded transcription. */
  const char *text;
  /* The audio data for the current phrase. */
  const float *audio_data;
  /* The number of elements in the audio data array. */
  size_t audio_data_count;
  /* Time offset from the start of the array or stream in seconds.  */
  float start_time;
  /* How long the segment currently is in seconds. */
  float duration;
  /* Stable identifier for the line. */
  uint64_t id;
  /* Streaming-only: Zero means the speaker hasn't finished talking in this
   * segment, non-zero means they have. */
  int8_t is_complete;
  /* Streaming-only: Whether the line has been updated since the previous call
   * to moonshine_transcribe_stream. */
  int8_t is_updated;
  /* Streaming-only: Whether the line was newly added since the previous call to
   * moonshine_transcribe_stream. */
  int8_t is_new;
  /* Streaming-only: Whether the text of the line has changed since the previous
   * call to moonshine_transcribe_stream. */
  int8_t has_text_changed;
  /* Whether the speaker spans of the line have changed since the previous
   * call to moonshine_transcribe_stream. Unlike the other change flags, this
   * can fire for lines that are already complete, since diarization refines
   * speaker assignments retroactively as more audio arrives. */
  int8_t have_speakers_changed;
  /* Speaker spans covering this line, ordered by start time and clipped to
   * the line's time range. NULL unless the identify_speakers option is
   * enabled and speech has been attributed to a speaker. */
  const struct speaker_span_t *speaker_spans;
  /* Number of entries in the speaker_spans array. */
  uint64_t speaker_span_count;
  /* Streaming-only: The latency of the last transcription in milliseconds. */
  uint32_t last_transcription_latency_ms;
  /* Word-level timestamps. NULL if word_timestamps option is not enabled. */
  const struct transcript_word_t *words;
  /* Number of words in the words array. 0 if not enabled. */
  uint64_t word_count;
};

/* An entire transcription of an audio data array or stream.                 */
struct transcript_t {
  struct transcript_line_t *lines; /* All lines of the transcript. */
  uint64_t line_count;             /* Number of lines in the transcript.      */
};

/* ------------------------------ FUNCTIONS -------------------------------- */

/* Returns the loaded moonshine library version. This may be different from
   the header version if a newer shared library is loaded.
*/
MOONSHINE_EXPORT int32_t moonshine_get_version(void);

/* Converts an error code number returned from an API call into a
   human-readable string. */
MOONSHINE_EXPORT const char *moonshine_error_to_string(int32_t error);

/* Frees a buffer that a moonshine_* function documented as "allocated with
   malloc; release with free" returned to the caller. This covers, for
   example, ``out_audio_data`` from moonshine_text_to_speech /
   moonshine_phonemes_to_speech, the JSON / comma-separated strings from
   moonshine_get_tts_dependencies / moonshine_get_g2p_dependencies /
   moonshine_get_tts_voices, and ``out_phonemes`` from
   moonshine_text_to_phonemes.

   Always use this instead of the C runtime ``free`` directly. On Windows the
   library and its host (e.g. a Python binding) can be linked against
   different C runtimes with independent heaps, so freeing a library-allocated
   pointer with the host's ``free`` corrupts the heap. Routing the free back
   through the library guarantees the allocation and deallocation happen in
   the same runtime. Safe to call on NULL. */
MOONSHINE_EXPORT void moonshine_free_buffer(void *ptr);

/* Replaces the contextual-biasing key terms on an existing transcriber, so a
   caller can follow whatever context the user is in - the contact list on
   screen, the vocabulary of the document being dictated into - without
   reloading the model. ``keyterms`` is a comma-separated list using the same
   syntax as the ``keyterms`` load option; pass NULL or an empty string to turn
   biasing off.

   Safe to call between transcribe calls on a live stream. Takes effect on the
   next transcribe call: it does not retroactively change text already emitted.

   Returns ``MOONSHINE_ERROR_NONE`` on success, or a non-zero error code if the
   handle is invalid or the loaded model is not a streaming architecture (only
   those decode through a path that can apply the bias). */
MOONSHINE_EXPORT int32_t moonshine_transcriber_set_keyterms(
    int32_t transcriber_handle, const char *keyterms);

/* Picks the key terms out of a passage of free-form text and biases towards
   them, replacing any previous list. Where
   ``moonshine_transcriber_set_keyterms`` wants a list, this wants context: hand
   over the document on screen, the agenda for the meeting, the last few
   messages in the thread, and the unusual words in it are found for you.

   A word is judged unusual by how the model's own tokenizer spells it. That
   vocabulary is ordered by frequency, so an everyday word has a token to itself
   while jargon and proper nouns have to be built out of several subwords, and
   needing more than one is the signal used here. It follows the language of the
   loaded model, and the capitalization in the passage is what gets asked for in
   the transcript.

   ``max_terms`` caps the list; pass 0 for the default of 200. The cap matters:
   a long list costs accuracy on the words you did not ask for (see
   docs/models/domain-customization.md), so the terms the passage leans on
   hardest are kept and the rest of its long tail is dropped. Pass NULL or an
   empty string to turn biasing off.

   Safe to call between transcribe calls on a live stream. Takes effect on the
   next transcribe call: it does not retroactively change text already emitted.

   Returns ``MOONSHINE_ERROR_NONE`` on success, or a non-zero error code if the
   handle is invalid or the loaded model is not a streaming architecture (only
   those decode through a path that can apply the bias). */
MOONSHINE_EXPORT int32_t moonshine_transcriber_set_context(
    int32_t transcriber_handle, const char *context, int32_t max_terms);

/* Converts a transcript_t struct into a human-readable string for debugging
 * purposes. The string is owned by the library, and is valid until the next
 * call to moonshine_transcript_to_string. */
MOONSHINE_EXPORT const char *moonshine_transcript_to_string(
    const struct transcript_t *transcript);

/* Loads models from the file system, using `path` as the root directory. The
   implementation expects the following files to be present in the directory:
   - encoder_model.ort
   - decoder_model_merged.ort
   - tokenizer.bin
   The .ort files are quantized activation ONNX models that have been converted
   to ORT format using the onnxruntime tools. The simplest way to obtain these
   files is to run the `scripts/download-moonshine-model.py` script, for
   example `python scripts/download-moonshine-model.py --model-type base
   --model-language en`.
   The source weights are available on the Hugging Face Model Hub at
   https://huggingface.co/moonshine-ai/, and the download and conversion to
   ONNX script is available in this repository at
   `scripts/convert-moonshine-model.sh`.
   The tokenizer.bin contains the token to character mapping for the model,
   in a compact binary format. The `scripts/json-to-bin-vocab.py` can be used
   to convert common tokenizer.json files to tokenizer.bin files.

   The `model_arch` parameter is used to select the model architecture, for
   example MOONSHINE_MODEL_ARCH_BASE or MOONSHINE_MODEL_ARCH_TINY_STREAMING.

   The `options` parameter is used to set any custom options for the
   transcriber. Recognized options include ``log_ort_run`` (bool),
   ``ort_providers`` (comma-separated execution provider names such as
   ``CoreML,CPU`` on macOS; default and recommendation is CPU-only, and the
   iOS and Android libraries ship with no other choice — see
   docs/execution-providers.md), and ``coreml_cache_dir`` (directory for the
   CoreML compiled model cache on macOS).
   Pass ``use_speculative_decoding`` (bool, default true) to control
   speculative re-decode of the previous hypothesis on streaming updates
   (set false to fall back to greedy redecode from BOS).
   Pass ``decode_incomplete_lines`` (bool, default true) to run the
   decoder on in-progress lines so the transcript can update while someone
   is still talking. Set false to encode (and diarize) as audio arrives
   but wait until the line is complete before decoding.
   Pass ``keyterms`` (comma-separated terms, e.g.
   ``Kubernetes,Anushka Sharma,ANSI/ISO``) to bias the decoder towards words it
   would otherwise be unlikely to produce - jargon, product names, contact
   names. No retraining is involved: each term is compiled into a subword trie
   and used to nudge the decoder's logits, so the terms can be different on
   every transcriber and can be replaced mid-stream with
   ``moonshine_transcriber_set_keyterms``. Match the capitalization and
   spelling you want to see in the output. Only the streaming architectures
   apply this. Pass ``context`` instead (or as well) to hand over a passage of
   free-form text and have the terms picked out of it, as
   ``moonshine_transcriber_set_context`` does, with ``context_max_terms``
   (int, default 200) capping how many are taken.
   ``keyterm_boost`` (float, default 2.0) sets the strength. The
   default is where the terms come out most accurately; going higher recovers no
   more of them and starts putting them where they were not said, so lower it if
   general accuracy matters more than the list does, rather than raising it.
   Pass ``identify_speakers`` (bool, default false) to enable speaker
   diarization: each line then carries a ``speaker_spans`` array describing
   who spoke when, including UTF-8 character ranges into the line text.
   This also enables word timestamps automatically. This runs the cpp-annote
   diarization pipeline (a port of
   pyannote community-1) inline inside transcription calls, which adds
   significant compute, and re-clustering cost grows with session length unless
   bounded by ``diarization_cluster_window_sec``.
   ``diarization_cluster_cadence`` (float seconds, default 2.0) sets the
   minimum interval between re-clustering passes - raise it to reduce cost on
   long sessions - ``diarization_analyze_cadence`` (float seconds,
   default 0 = model default of 1.0) sets the sliding-window step between
   segmentation/embedding model runs (live ``add_audio`` / transcribe runs at
   most one window per call; Stop drains the rest; silent speaker classes skip
   embedding inference), and ``diarization_cluster_window_sec``
   (float seconds, default 120.0) limits how much audio history VBx
   re-clustering considers on each refresh (0 = unlimited full history).
   Pass ``"spelling_model_path"`` with a path to a
   spelling-CNN ``.ort`` file (e.g.
   ``https://download.moonshine.ai/model/spelling-en/spelling_cnn.ort``)
   to enable alphanumeric spelling fusion via
   ``MOONSHINE_FLAG_SPELLING_MODE``; if not set, the spelling model is
   not loaded and the flag is a no-op.

   The `options_count` parameter is the number of options in the options array.

   The `moonshine_version` parameter should be set to MOONSHINE_HEADER_VERSION
   to ensure that if a newer version of the library is loaded, it emulates the
   behavior of the older version to ensure compatibility.

   The return value is a handle to a transcriber, which can be used to identify
   the transcriber in subsequent calls. If there was an error, a negative value
   is returned. This code can be converted to a human-readable string using
   moonshine_error_to_string.
*/
MOONSHINE_EXPORT int32_t moonshine_load_transcriber_from_files(
    const char *path, uint32_t model_arch,
    const struct moonshine_option_t *options, uint64_t options_count,
    int32_t moonshine_version);

/* **DEPRECATED** Use moonshine_load_transcriber_from_memory_files instead.
   This function is deprecated and will be removed in a future version.

   Callers that pass a `moonshine_version` of
   MOONSHINE_FROM_MEMORY_REMOVED_VERSION or newer are refused: the call logs an
   explanation and returns MOONSHINE_ERROR_INVALID_ARGUMENT without loading
   anything. Only clients built against an earlier header, which pass that
   earlier version here, can still use it.

   Loads models from memory. The `encoder_model_data`, `decoder_model_data` and
   `tokenizer_data` parameters are the data arrays for the models in binary
   format, and are expected to be in the same format as the files disk.

   `spelling_model_data` and `spelling_model_data_size` are an optional
   in-memory ``.ort`` payload for the alphanumeric spelling-CNN. Pass
   ``NULL`` and ``0`` if you don't want spelling fusion. When provided,
   the buffer must outlive the transcriber (it is *not* copied) and the
   transcriber will run spelling fusion whenever
   ``MOONSHINE_FLAG_SPELLING_MODE`` is passed to
   ``moonshine_transcribe_stream`` or
   ``moonshine_transcribe_without_streaming``.

   All of the other parameters are the same as for
   moonshine_load_transcriber_from_files.                                    */
MOONSHINE_EXPORT int32_t moonshine_load_transcriber_from_memory(
