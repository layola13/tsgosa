const s = "";
if ((s ?? "d") || 0) {
  console.log(1);
} else {
  console.log(0);
}
if (!(s ?? "d")) {
  console.log(3);
} else {
  console.log(4);
}
