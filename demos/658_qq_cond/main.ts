const s = "hi";
const e = "";
if (s ?? "d") {
  console.log(1);
} else {
  console.log(0);
}
if (e ?? "d") {
  console.log(3);
} else {
  console.log(4);
}
