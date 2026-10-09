function gs(): string {
  return "q";
}
if (gs() != null) {
  console.log(1);
} else {
  console.log(0);
}
if (gs() == null) {
  console.log(3);
} else {
  console.log(4);
}
