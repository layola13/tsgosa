const n = 2;
const s = "x";
const b = true;
const e = "";
if (n && s) {
  console.log(1);
} else {
  console.log(0);
}
if (b && e) {
  console.log(1);
} else {
  console.log(0);
}
if (1 && s) {
  console.log(3);
}
if (0 || s) {
  console.log(4);
}
