import { render } from "preact";
import { App } from "./app";
import { checkExistingSession } from "./auth/cognito";
import "./style.css";

// Check for existing Cognito session before rendering (DDR-028)
checkExistingSession().then(() => {
  render(<App />, document.getElementById("app")!);
});
