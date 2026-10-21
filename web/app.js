const list = document.getElementById("playlist");
const fileInput = document.getElementById("file");
const drop = document.getElementById("drop");
const now = document.getElementById("now");
const messages = document.getElementById("messages");
const preview = document.getElementById("preview");
const confirmDialog = document.getElementById("confirm");
const confirmName = document.getElementById("confirm-name");
let dragging = null;

const styles = {
  info: "border-zinc-300",
  ok: "border-kmvt-green",
  error: "border-red-600 text-red-700",
};

const tag = "inline-flex h-7 items-center rounded border px-2.5 font-heading text-xs font-bold";

async function load() {
  if (dragging) return;
  const res = await fetch("/api/playlist");
  const { videos, playing } = await res.json();
  now.textContent = playing || "Nothing";
  list.replaceChildren(...videos.map(name => row(name, name === playing)));
}

function row(name, playing) {
  const li = document.createElement("li");
  li.draggable = true;
  li.dataset.name = name;
  li.className = "flex cursor-move items-center gap-3 rounded-lg border border-zinc-200 px-4 py-3 hover:border-zinc-300";
  if (playing) li.classList.add("bg-zinc-50");

  const label = document.createElement("span");
  label.className = "flex-1 truncate";
  label.textContent = name;
  li.append(label);

  if (playing) {
    const badge = document.createElement("span");
    badge.className = tag + " border-red-600 bg-red-600 text-white";
    badge.textContent = "On Air";
    li.append(badge);
  }

  const remove = document.createElement("button");
  remove.className = tag + " border-zinc-300 text-zinc-600 hover:bg-zinc-100";
  remove.textContent = "Delete";
  remove.onclick = async () => {
    if (!(await confirmDelete(name))) return;
    await send("DELETE", "/api/videos/" + encodeURIComponent(name));
    load();
  };
  li.append(remove);

  return li;
}

function confirmDelete(name) {
  confirmName.textContent = name;
  confirmDialog.returnValue = "";
  confirmDialog.showModal();
  return new Promise(resolve => {
    confirmDialog.addEventListener("close", () => resolve(confirmDialog.returnValue === "delete"), { once: true });
  });
}

confirmDialog.addEventListener("click", e => {
  if (e.target === confirmDialog) confirmDialog.close();
});

function message(text, style) {
  const li = document.createElement("li");
  messages.append(li);
  setMessage(li, text, style);
  return li;
}

function setMessage(li, text, style) {
  li.className = "rounded border-l-4 bg-zinc-50 px-4 py-2 text-sm " + styles[style];
  li.textContent = text;
}

async function send(method, url, body) {
  const res = await fetch(url, { method, body });
  if (!res.ok) message(await res.text(), "error");
}

list.addEventListener("dragstart", e => {
  dragging = e.target;
  dragging.classList.add("opacity-50");
  e.dataTransfer.setData("text/plain", "");
});

list.addEventListener("dragover", e => {
  e.preventDefault();
  const target = e.target.closest("li");
  if (!target || target === dragging) return;
  const box = target.getBoundingClientRect();
  if (e.clientY > box.top + box.height / 2) target.after(dragging);
  else target.before(dragging);
});

list.addEventListener("dragend", async () => {
  dragging.classList.remove("opacity-50");
  dragging = null;
  const names = [...list.children].map(li => li.dataset.name);
  await send("PUT", "/api/playlist", JSON.stringify(names));
  load();
});

fileInput.addEventListener("change", async () => {
  await uploadAll(fileInput.files);
  fileInput.value = "";
});

window.addEventListener("dragover", e => e.preventDefault());
window.addEventListener("drop", e => e.preventDefault());

drop.addEventListener("dragover", () => drop.classList.add("bg-zinc-50"));
drop.addEventListener("dragleave", () => drop.classList.remove("bg-zinc-50"));
drop.addEventListener("drop", e => {
  drop.classList.remove("bg-zinc-50");
  uploadAll(e.dataTransfer.files);
});

async function uploadAll(files) {
  messages.replaceChildren();
  for (const file of files) await upload(file);
  load();
}

function upload(file) {
  if (!file.name.toLowerCase().endsWith(".mp4")) {
    message(`${file.name} was not uploaded: only .mp4 files are accepted`, "error");
    return;
  }

  const item = message(`Uploading ${file.name}`, "info");
  const bar = document.createElement("progress");
  bar.max = 100;
  bar.value = 0;
  bar.className = "mt-2 block h-1.5 w-full accent-kmvt-purple";
  item.append(bar);

  return new Promise(resolve => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/videos/" + encodeURIComponent(file.name));
    xhr.upload.onprogress = e => bar.value = e.loaded / e.total * 100;
    xhr.upload.onload = () => item.firstChild.textContent = `Checking ${file.name}`;
    xhr.onload = () => {
      if (xhr.status === 200) setMessage(item, `Uploaded ${file.name}`, "ok");
      else setMessage(item, `${file.name} was rejected: ${xhr.responseText}`, "error");
      resolve();
    };
    xhr.onerror = () => {
      setMessage(item, `${file.name} failed to upload: lost connection to the server`, "error");
      resolve();
    };
    xhr.send(file);
  });
}

function startPreview() {
  if (!Hls.isSupported()) {
    preview.src = "/preview/live.m3u8";
    return;
  }
  const hls = new Hls();
  hls.on(Hls.Events.ERROR, (event, data) => {
    if (!data.fatal) return;
    hls.destroy();
    setTimeout(startPreview, 5000);
  });
  hls.loadSource("/preview/live.m3u8");
  hls.attachMedia(preview);
}

load();
setInterval(load, 5000);
startPreview();
