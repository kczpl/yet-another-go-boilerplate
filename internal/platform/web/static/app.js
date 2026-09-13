"use strict";

// Show a message when a request fails. Keep the form values intact.
function showRequestError() {
  const notice = document.getElementById("request-error");
  notice.textContent = "The request failed. Check your connection or reload the page before you try again.";
  notice.hidden = false;
}

document.addEventListener("htmx:responseError", showRequestError);
document.addEventListener("htmx:sendError", showRequestError);
document.addEventListener("htmx:timeout", showRequestError);
document.addEventListener("htmx:afterRequest", function (event) {
  if (event.detail.successful) {
    document.getElementById("request-error").hidden = true;
  }
});
