const a = "x";
if ((a + "y") != null) {
  console.log(1);
} else {
  console.log(0);
}
if ((a + "y") == null) {
  console.log(3);
} else {
  console.log(4);
}
