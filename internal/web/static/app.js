// Small progressive enhancements; every page works without JS.
(function () {
  // Upload as soon as a photo is chosen. HEIC/HEIF (e.g. from Files) is converted to JPEG in
  // the browser when it can decode it (Safari); otherwise it is sent as-is and the server
  // converts it or explains what to do.
  document.querySelectorAll("input[data-autosubmit]").forEach(function (inp) {
    inp.addEventListener("change", async function () {
      var file = inp.files && inp.files[0];
      if (!file) return;
      var form = inp.form;
      var hint = form.querySelector(".hint");
      if (hint) hint.textContent = "Uploading…";
      var isHeic = /heic|heif/i.test(file.type) || /\.(heic|heif)$/i.test(file.name);
      if (isHeic && window.createImageBitmap) {
        try {
          var bmp = await createImageBitmap(file);
          var c = document.createElement("canvas");
          c.width = bmp.width; c.height = bmp.height;
          c.getContext("2d").drawImage(bmp, 0, 0);
          var blob = await new Promise(function (res) { c.toBlob(res, "image/jpeg", 0.9); });
          if (blob) {
            var fd = new FormData();
            fd.append("image", blob, file.name.replace(/\.(heic|heif)$/i, "") + ".jpg");
            var resp = await fetch(form.action, { method: "POST", body: fd });
            document.open(); document.write(await resp.text()); document.close();
            if (resp.redirected) history.replaceState(null, "", resp.url);
            return;
          }
        } catch (e) { /* fall through to a normal upload */ }
      }
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
