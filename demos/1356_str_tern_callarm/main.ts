function ex(n: i32): string {
  return n > 0 ? "p" : "n";
}
function main(): i32 {
  console.log(true ? ex(1) : "x");
  return 0;
}
