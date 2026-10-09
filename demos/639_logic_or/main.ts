const s = "hi";
const e = "";
const n = 0;
if (s || n) {
  console.log(1);
} else {
  console.log(0);
}
if (e || n) {
  console.log(1);
} else {
  console.log(0);
}
