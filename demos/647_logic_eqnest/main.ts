const a = "x";
const b = "";
const n = 1;
if ((a && n) || b) {
  console.log(1);
} else {
  console.log(0);
}
if ((b || n) && a) {
  console.log(3);
} else {
  console.log(4);
}
if (a == "x" && n) {
  console.log(5);
}
