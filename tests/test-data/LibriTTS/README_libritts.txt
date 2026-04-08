1. General information
======================

The LibriSpeech ASR corpus (http://www.openslr.org/12/) [1] has been used in various research projects. However, as it was originally designed for ASR research, there are some undesired properties when using for TTS research, such as

* The audio files are at 16 kHz sampling rate; 16 kHz sampling is high enough for ASR purpose but too low to achieve high quality TTS.  Modern production-quality TTS systems often use 24, 32, 44.1, or 48 kHz sampling rate.
* The speech is split at silence intervals}; The training data speech is split at silences longer than 0.3 seconds.   To learn long-term characteristics of speech such as the sentence-level prosody for given a text, it is necessary to split speech at sentence breaks.
* All letters are normalized into uppercase, and all punctuation is removed; Capitalization and punctuation marks are useful features to learn prosodic characteristics such as emphasis and the length of pauses.
* The position of sentences within paragraphs is discarded; To learn inter-sentence prosody it is desirable to access neighbouring sentences or audio but this information is missing.
* Some audio files contain significant background noise even within its "clean" subsets; In the LibriSpeech corpus, speakers with low word error rates (WERs) using the Wall Street Journal (WSJ) acoustic model were designated as "clean".  Therefore, the "clean" subset can contain noisy samples.

The LibriTTS corpus aims to address these issues while keeping the desired properties of LibriSpeech.  As LibriTTS is derived from the original materials (mp3 & text) of the LibriSpeech corpus, it has the same speakers/subsets as LibriSpeech.  This corpus has the following properties:

* The audio files are at 24kHz sampling rate; As most of the original material is recorded at 44.1 or 32kHz sampling rate, all audio with a sampling rate of less than 24kHz were excluded (two 16kHz files and six 22.05kHz files).
* The speech is split at sentence breaks; Text is split using Google's proprietary sentence splitting engine then audio was then split at these sentence boundaries.
* Both original and normalized texts are included; The text has been normalized using Google's proprietary text normalization engine.
* Contextual information (e.g., neighbouring sentences) can also be extracted; Additional text files provide easy access to neighbouring sentences.
* Utterances with significant background noise are excluded}; Utterance-level signal-to-noise ratio (SNR) was estimated and used to filter out noisy lines.

Text was first normalized by Google's proprietary TTS text normalization system.  Then matching between normalized text / audio was carried out via YouTube AutoSync system [2].  All unaligned lines were filtered out. Utterances with background noise were also excluded based on the signal-to-noise ratio (SNR) computed by the WADA algorithm [3]. The "clean" subsets in the LibriTTS corpus contain audio with WADA-SNR >= 20dB only, whereas the "other" subsets contain audio with WADA-SNR >= 0dB.

Here are aggregated statistics of the LibriTTS corpus.

subset           total duration (hours)  number of speakers
-----------------------------------------------------------
train-clean-100        53.78                   247
train-clean-360       191.29                   904
train-other-500       310.08                  1160
dev-clean               8.97                    40
dev-other               6.43                    33
test-clean              8.56                    39
test-other              6.69                    33
-----------------------------------------------------------
total                 585.80                  2456


2. Structure
============

The directory structure is compatible with the LibriSpeech corpus.

When extracted, each of the {dev,test,train} sets re-creates LibriTTS's root directory, containing a dedicated subdirectory for the subset itself. The audio for each individual speaker is stored under a dedicated  subdirectory in the subset's directory, and each audio chapter read by this speaker is stored in separate subsubdirectory. The following ASCII diagram depicts the directory structure:

LibriTTS
    |
    .- README_librispeech.txt
    |
    .- README_libritts.txt
    |
    .- SPEAKER.txt
    |
    .- CHAPTERS.txt
    |
    .- BOOKS.txt
    |
    .- LICENSE.txt
    |
    .- NOTE.txt
    |
    .- eval_sentences10.tsv
    |
    .- reader_book.tsv
    |
    .- speakers.tsv
    |
    .- train-clean-100/
                   |
                   .- 19/
                       |
                       .- 198/
                       |    |
                       |    .- 19_198.book.tsv
                       |    |
                       |    .- 19_198.trans.tsv
                       |    |
                       |    .- 19_198_000000_000000.normalized.txt
                       |    |
                       |    .- 19_198_000000_000000.original.txt
                       |    |
                       |    .- 19_198_000000_000000.wav
                       |    |
                       |    .- 19_198_000000_000002.normalized.txt
                       |    |
                       |    ...
                       |
                       .- 227/
                            | ...



where 19 is the ID of the reader, and 198 and 227 are the IDs of the chapters read by this speaker. The *.book.tsv and trans.tsv files are TSV files contain the details of chapter and transcripts for each utterance, respectively.

The structure of *.trans.tsv files is as follows:

# ID of utterance	Original text	Normalized text
19_198_000000_000000	This is a LibriVox recording.	This is a LibriVox recording.
19_198_000000_000002	For more information, or to volunteer, please visit librivox.org.	For more information, or to volunteer, please visit librivox dot org.


The structure of *.book.tsv files is as follows.

# ID of utterance	Original text	Normalized text	Aligned or not	Start time of this utterance in original mp3 file (in second)	End time of this utterance in original mp3 file (in second)	Signal-to-noise ratio for this utterance
19_198_000000_000000	This is a LibriVox recording.	This is a LibriVox recording.	true	1.95	4.18	20.152367
19_198_000000_000001	All LibriVox recordings are in the public domain.	All LibriVox recordings are in the public domain.	true	4.7	7.81	19.710249


*.trans.tsv files contain transcripts for generated files, whereas *.book.tsv files contain transcripts for excluded lines as well.


eval_sentences10.tsv file aims to provide a set of sentences to be used for subjective evaluation.  These sentences were selected from utterances in test-* subsets; 10 utterances were randomly selected per speaker.


The main metainfo about the speech is listed in the READERS, CHAPTERS, and BOOKS.

* SPEAKERS.txt contains information about speaker's gender and total amount of
  audio in the corpus.
* CHAPTERS.TXT has information about the per-chapter audio durations.

The file BOOKS.TXT makes contains the title for each book, whose text is used in the corpus, and its Project Gutenberg ID.

Please also refer to README_librispeech.txt for details.


Acknowledgments
===============

The authors would like to thank Drs. Hank Liao and Hagen Soltau for their helpful comments about YouTube's auto-sync feature.  We also thank Mr. Wei-Ning Hsu, Ms. Daisy Stanton, Mr. RJ Skerry-Ryan, and Dr. Yuxuan Wang for helpful comments about the corpus.  We also would like to express our gratitude to Drs. Guoguo Chen, Sanjeev Khudanpur, Vassil Panayotov, and Daniel Povey for releasing the LibriSpeech corpus, and to the thousands of Project Gutenberg and LibriVox volunteers.

References
==========
[1] Vassil Panayotov, Guoguo Chen, Daniel Povey and Sanjeev Khudanpur, "LibriSpeech: An ASR corpus based on public domain audio books", ICASSP, 2015.
[2] Hank Liao, Erik McDermott and Andrew Senior, "Large scale deep neural network acoustic modeling with semi-supervised training data for YouTube video transcription", ASRU, 2013.
[3] Chanwoo Kim and Richard Stern, "Robust signal-to-noise ratio estimation based on waveform amplitude distribution analysis," Interspeech, 2008.

---
Heiga Zen, Viet Dang, Rob Clerk, Yu Zhang, Ron J. Weiss, Ye Jia, Zhifeng Chen, Yonghui Wu
25th March, 2019.
