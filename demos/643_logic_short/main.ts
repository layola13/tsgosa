function eff(): i32 {
  console.log(9);
  return 1;
}
const e = "";
const s = "hi";
if (e && eff()) {
  console.log(1);
} else {
  console.log(0);
}
if (s || eff()) {
  console.log(3);
} else {
  console.log(4);
}
