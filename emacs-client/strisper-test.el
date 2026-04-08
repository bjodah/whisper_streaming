;;; strisper-test.el --- ERT tests for strisper -*- lexical-binding: t; -*-

;;; Commentary:

;; Focused regression tests for the interactive Emacs client.

;;; Code:

(require 'cl-lib)
(require 'ert)
(require 'strisper)

(ert-deftest strisper-process-filter-parses-complete-lines ()
  (let ((proc-buffer (generate-new-buffer " *strisper-test-proc*"))
        (target-buffer (generate-new-buffer " *strisper-test-target*"))
        (strisper--target-buffer nil)
        (strisper--insert-marker nil)
        (strisper--rec-proc nil))
    (unwind-protect
        (let ((proc-mark (with-current-buffer proc-buffer
                           (copy-marker (point-min))))
              (fake-process 'fake-process))
          (with-current-buffer target-buffer
            (insert "Prefix: ")
            (setq strisper--target-buffer target-buffer)
            (setq strisper--insert-marker (point-marker)))
          (cl-letf (((symbol-function 'process-buffer)
                     (lambda (_process) proc-buffer))
                    ((symbol-function 'process-mark)
                     (lambda (_process) proc-mark)))
            (strisper--process-filter fake-process "0 1200 Hello\n1200 1850 wo")
            (should (equal (with-current-buffer target-buffer
                             (buffer-string))
                           "Prefix: Hello "))
            (strisper--process-filter fake-process "rld\n1850 2200 too  many  spaces\n")
            (should (equal (with-current-buffer target-buffer
                             (buffer-string))
                           "Prefix: Hello world too many spaces "))
            (with-current-buffer proc-buffer
              (should (= strisper--parse-pos (point-max))))))
      (kill-buffer proc-buffer)
      (kill-buffer target-buffer))))

(ert-deftest strisper-start-process-initializes-state ()
  (let ((stdout-buffer (get-buffer-create "*strisper-stdout*"))
        (stderr-buffer (get-buffer-create "*strisper-stderr*"))
        (target-buffer (generate-new-buffer " *strisper-test-target*"))
        (strisper--target-buffer nil)
        (strisper--insert-marker nil)
        (strisper--rec-proc nil)
        (strisper-record-command "printf test")
        captured-args)
    (unwind-protect
        (progn
          (with-current-buffer stdout-buffer
            (insert "stale output")
            (setq-local strisper--parse-pos (point-max)))
          (with-current-buffer target-buffer
            (insert "Prefix: ")
            (cl-letf (((symbol-function 'make-process)
                       (lambda (&rest args)
                         (setq captured-args args)
                         'fake-process)))
              (strisper--start-process target-buffer)))
          (should (eq strisper--rec-proc 'fake-process))
          (should (eq strisper--target-buffer target-buffer))
          (should (eq (marker-buffer strisper--insert-marker) target-buffer))
          (with-current-buffer stdout-buffer
            (should (equal (buffer-string) ""))
            (should (= strisper--parse-pos (point-min))))
          (should (equal (plist-get captured-args :command)
                         (list "sh" "-c" strisper-record-command)))
          (should (eq (plist-get captured-args :buffer) stdout-buffer))
          (should (eq (plist-get captured-args :stderr) stderr-buffer))
          (should (eq (plist-get captured-args :filter)
                      #'strisper--process-filter)))
      (kill-buffer target-buffer)
      (kill-buffer stdout-buffer)
      (kill-buffer stderr-buffer))))

(ert-deftest strisper-stop-clears-state ()
  (let ((marker-buffer (generate-new-buffer " *strisper-test-marker*"))
        marker
        (strisper--target-buffer (get-buffer-create " *strisper-target*"))
        (strisper--rec-proc 'fake-process)
        (strisper--insert-marker nil)
        killed-process)
    (with-current-buffer marker-buffer
      (insert "target")
      (setq marker (point-marker))
      (setq strisper--insert-marker marker))
    (unwind-protect
        (cl-letf (((symbol-function 'process-live-p)
                   (lambda (_process) t))
                  ((symbol-function 'kill-process)
                   (lambda (process)
                     (setq killed-process process))))
          (strisper-stop)
          (should (eq killed-process 'fake-process))
          (should (null strisper--rec-proc))
          (should (null strisper--target-buffer))
          (should (null strisper--insert-marker))
          (should (null (marker-buffer marker))))
      (when (buffer-live-p strisper--target-buffer)
        (kill-buffer strisper--target-buffer))
      (when (buffer-live-p marker-buffer)
        (kill-buffer marker-buffer)))))

(ert-deftest strisper-record-at-point-uses-current-buffer ()
  (let (captured-buffer)
    (with-temp-buffer
      (cl-letf (((symbol-function 'strisper--ensure-started)
                 (lambda (buffer)
                   (setq captured-buffer buffer))))
        (strisper-record-at-point)
        (should (eq captured-buffer (current-buffer)))))))

(provide 'strisper-test)

;;; strisper-test.el ends here
