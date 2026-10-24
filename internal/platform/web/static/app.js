"use strict";

// Show a message in the error notice of the layout.
function showError(message) {
  const notice = document.getElementById("request-error");
  notice.textContent = message;
  notice.hidden = false;
}

function hideError() {
  document.getElementById("request-error").hidden = true;
}

// Show a message when a request fails. Keep the form values intact.
function showRequestError() {
  showError("The request failed. Check your connection or reload the page before you try again.");
}

document.addEventListener("htmx:responseError", showRequestError);
document.addEventListener("htmx:sendError", showRequestError);
document.addEventListener("htmx:timeout", showRequestError);
document.addEventListener("htmx:afterRequest", function (event) {
  if (event.detail.successful) {
    hideError();
  }
});

// A form with data-stream posts with fetch and streams the answer into the
// element that data-stream selects. Without JavaScript, or when that
// element does not exist, the form posts normally and the server renders
// the complete answer.
document.addEventListener("submit", function (event) {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || !form.dataset.stream) {
    return;
  }
  const output = document.querySelector(form.dataset.stream);
  if (output) {
    event.preventDefault();
    streamForm(form, output);
  }
});

async function streamForm(form, output) {
  const button = form.querySelector("button[type=submit]");
  button.disabled = true;
  hideError();
  output.textContent = "";
  output.setAttribute("aria-busy", "true");
  try {
    const response = await fetch(form.action, {
      method: "POST",
      headers: { Accept: "text/event-stream" },
      body: new URLSearchParams(new FormData(form)),
    });
    const type = response.headers.get("Content-Type") || "";
    if (!response.ok || !type.startsWith("text/event-stream")) {
      // The server did not start a stream, for example after a rate limit
      // or an expired session. The model did not run. A normal post shows
      // the full page for this case.
      form.submit();
      return;
    }
    if (!(await readAnswer(response, output))) {
      showError("The answer stopped early. Try again.");
    }
  } catch {
    showRequestError();
  } finally {
    button.disabled = false;
    output.removeAttribute("aria-busy");
  }
}

// Add each "delta" event to output as text. Return true when the stream
// ends with "done" or "error", and false when the stream stops early.
async function readAnswer(response, output) {
  for await (const event of readEvents(response)) {
    if (event.type === "delta") {
      // append() adds a text node, so the model text never becomes HTML.
      output.append(event.data);
    } else if (event.type === "error") {
      showError(event.data);
      return true;
    } else if (event.type === "done") {
      return true;
    }
  }
  return false;
}

// Parse a text/event-stream body and yield one {type, data} object per
// event. Only the "event" and "data" fields are read, because the server
// (web.EventStream) sends only these fields.
async function* readEvents(response) {
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) {
      return;
    }
    buffer += value;
    // An empty line ends an event.
    let end;
    while ((end = buffer.indexOf("\n\n")) >= 0) {
      yield parseEvent(buffer.slice(0, end));
      buffer = buffer.slice(end + 2);
    }
  }
}

function parseEvent(block) {
  let type = "message";
  const data = [];
  for (const line of block.split("\n")) {
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? "" : line.slice(colon + 1);
    // The format allows one space after the colon.
    if (value.startsWith(" ")) {
      value = value.slice(1);
    }
    if (field === "event") {
      type = value;
    } else if (field === "data") {
      data.push(value);
    }
  }
  return { type: type, data: data.join("\n") };
}
