import { render } from "preact";
import { App } from "./app";
import { checkExistingSession } from "./auth/cognito";
import { initRouter } from "./router";
import "./style.css";

// Check for existing Cognito session before rendering (DDR-028)
checkExistingSession().then(() => {
  // Restore step / workflow / sessionId from the URL before first render (DDR-056)
  initRouter();
  render(<App />, document.getElementById("app")!);
});
