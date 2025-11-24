const rows = document.getElementById("rows");
const empty = document.getElementById("empty");
const drop = document.getElementById("drop");
const picker = document.getElementById("picker");
const progress = document.getElementById("progress");
const status = document.getElementById("status");

function humanSize(n) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return (i === 0 ? n : n.toFixed(1)) + " " + units[i];
}

async function refresh() {
  const res = await fetch("/api/files");
  if (res.status === 401) { location.href = "/login"; return; }
  const { files } = await res.json();
  rows.replaceChildren();
  empty.hidden = files.length > 0;
  for (const f of files) {
    const tr = document.createElement("tr");

    const name = document.createElement("td");
    const link = document.createElement("a");
    link.href = "/files/" + encodeURIComponent(f.name);
    link.textContent = f.name;
    name.append(link);

    const size = document.createElement("td");
    size.textContent = humanSize(f.size);

    const when = document.createElement("td");
    when.textContent = new Date(f.modified).toLocaleString();

    const actions = document.createElement("td");
    // The name opens the file in a tab when the browser can render it; this always saves it.
    const save = document.createElement("a");
    save.href = link.href + "?download=1";
    save.textContent = "Download";
    actions.append(save, " ");

    const del = document.createElement("button");
    del.className = "ghost";
    del.textContent = "Delete";
    del.onclick = () => remove(f.name);
    actions.append(del);

    tr.append(name, size, when, actions);
    rows.append(tr);
  }
}

async function remove(name) {
  if (!confirm(`Delete ${name}?`)) return;
  const res = await fetch("/api/delete", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  if (res.status === 401) { location.href = "/login"; return; }
  if (!res.ok) status.textContent = (await res.json()).error;
  refresh();
}

// XMLHttpRequest rather than fetch, because it is the one that reports upload progress.
function upload(files) {
  if (!files.length) return;
  const form = new FormData();
  for (const f of files) form.append("files", f, f.name);

  const xhr = new XMLHttpRequest();
  xhr.open("POST", "/api/upload");
  xhr.upload.onprogress = (e) => {
    if (!e.lengthComputable) return;
    progress.hidden = false;
    progress.value = (e.loaded / e.total) * 100;
  };
  xhr.onload = () => {
    progress.hidden = true;
    if (xhr.status === 401) { location.href = "/login"; return; }
    if (xhr.status === 200) {
      const { saved } = JSON.parse(xhr.responseText);
      status.textContent = `Uploaded ${saved.length} file${saved.length === 1 ? "" : "s"}.`;
    } else {
      let msg = xhr.responseText;
      try { msg = JSON.parse(xhr.responseText).error; } catch {}
      status.textContent = "Upload failed: " + msg;
    }
    refresh();
  };
  xhr.onerror = () => { progress.hidden = true; status.textContent = "Upload failed."; };
  status.textContent = "Uploading…";
  xhr.send(form);
}

picker.onchange = () => { upload(picker.files); picker.value = ""; };

for (const type of ["dragenter", "dragover"]) {
  drop.addEventListener(type, (e) => { e.preventDefault(); drop.classList.add("over"); });
}
for (const type of ["dragleave", "drop"]) {
  drop.addEventListener(type, (e) => { e.preventDefault(); drop.classList.remove("over"); });
}
drop.addEventListener("drop", (e) => upload(e.dataTransfer.files));

refresh();
