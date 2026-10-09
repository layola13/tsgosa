const s = "hi";
const e = "";
if (s?.length) {
  console.log(1);
}
if (e?.length) {
  console.log(2);
} else {
  console.log(3);
}
