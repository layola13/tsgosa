function grade(n: i32): string {
  return n >= 90 ? "A" : n >= 60 ? "B" : "C";
}
function main(): i32 {
  console.log(grade(95));
  console.log(grade(70));
  console.log(grade(30));
  return 0;
}
