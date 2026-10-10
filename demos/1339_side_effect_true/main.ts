let n: i32 = 0;
function bump(): string {
  n = n + 1;
  return "b";
}
function main(): i32 {
  console.log(true ? bump() : "x");
  console.log(n);
  return 0;
}
