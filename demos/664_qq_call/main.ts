function gs(): string {
  return "q";
}
const n = 1;
if (gs() ?? "d") {
  console.log(5);
}
if ((gs() ?? "d") && n) {
  console.log(6);
}
