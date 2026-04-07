# Integration tests

## Examples

Reference data is here:
```console
$ ./scripts/example-transcribe-upsream-service.sh test-data/LibriSpeech/2961-960-0021.wav 
{"text":"there is a want of flow and often a defect of rhythm the meaning is sometimes obscure and there is a greater use of apposition and more of repetition than occurs in plato's earlier writings"}
$ sed -n '22p' test-data/LibriSpeech/2961-960.trans.txt 
2961-960-0021 THERE IS A WANT OF FLOW AND OFTEN A DEFECT OF RHYTHM THE MEANING IS SOMETIMES OBSCURE AND THERE IS A GREATER USE OF APPOSITION AND MORE OF REPETITION THAN OCCURS IN PLATO'S EARLIER WRITINGS
```
Note how file 21 is found on row 22 (offset=1).
