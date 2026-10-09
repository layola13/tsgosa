const a = "x";
const b = "";
const n = 1;
if ((a ?? "d") && (b ?? "e")) {
  console.log(1);
} else {
  console.log(0);
}
if ((a ?? "d")) {
  console.log(3);
}
