// Small progressive enhancements; every page works without JS.
(function () {
  // Upload as soon as a photo is chosen. Files are sent untouched (HEIC included): the server
  // normalizes them to one JPEG, and the duplicate check needs the original bytes.
  document.querySelectorAll("input[data-autosubmit]").forEach(function (inp) {
    inp.addEventListener("change", function () {
      var file = inp.files && inp.files[0];
      if (!file) return;
      var form = inp.form;
      var hint = form.querySelector(".hint");
      if (hint) hint.textContent = "Uploading…";
      // only submit the input that has the file
      form.querySelectorAll("input[type=file]").forEach(function (o) { if (o !== inp) o.disabled = true; });
      form.submit();
    });
  });
  // Add an item row.
  document.querySelectorAll("[data-add-row]").forEach(function (b) {
    b.addEventListener("click", function () {
      var row = document.querySelector("#items tr.new");
      var copy = row.cloneNode(true);
      copy.querySelectorAll("input").forEach(function (i) { i.value = ""; });
      row.parentNode.appendChild(copy);
      copy.querySelector("input").focus();
    });
  });
  document.querySelectorAll("form[data-confirm]").forEach(function (f) {
    f.addEventListener("submit", function (e) { if (!confirm(f.dataset.confirm)) e.preventDefault(); });
  });
})();
