# Integration tests

## Examples

Reference data is here:
```console
$ ./scripts/example-transcribe-upsream-service.sh test-data/LibriSpeech/2961-960-0021.wav 
{"text":"there is a want of flow and often a defect of rhythm the meaning is sometimes obscure and there is a greater use of apposition and more of repetition than occurs in plato's earlier writings"}
$ sed -n '22p' test-data/LibriSpeech/2961-960.trans.txt 
2961-960-0021 THERE IS A WANT OF FLOW AND OFTEN A DEFECT OF RHYTHM THE MEANING IS SOMETIMES OBSCURE AND THERE IS A GREATER USE OF APPOSITION AND MORE OF REPETITION THAN OCCURS IN PLATO'S EARLIER WRITINGS
```
Note how file 21 is found on row 22 (offset=1). There is also some estimated timings for the parts of the sentances:
```console
$ cat test-data/LibriSpeech/2961-960-0021.timings.txt 

[00:00:00.000 --> 00:00:01.230]   there is a want
[00:00:01.230 --> 00:00:02.100]   of flow and
[00:00:02.100 --> 00:00:03.310]   often a defect
[00:00:03.310 --> 00:00:04.420]   of rhythm the
[00:00:04.420 --> 00:00:05.650]   meaning is
[00:00:05.650 --> 00:00:06.310]   sometimes
[00:00:06.310 --> 00:00:07.250]   obscure and
[00:00:07.250 --> 00:00:08.540]   there is a
[00:00:08.540 --> 00:00:09.260]   greater use of
[00:00:09.260 --> 00:00:10.570]   apposition and
[00:00:10.570 --> 00:00:11.170]   more of
[00:00:11.170 --> 00:00:12.740]   repetition than
[00:00:12.740 --> 00:00:13.890]   occurs in plato
[00:00:13.890 --> 00:00:14.800]  's earlier
[00:00:14.800 --> 00:00:15.780]   writings
```
As you can see, these files are all rather short. For them to constitute a good test case for a streaming transcription service the audio files need to be concatenated.
