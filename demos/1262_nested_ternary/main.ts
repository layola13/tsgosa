function grade(s: i32): i32 {
  return s >= 90 ? 1 : s >= 60 ? 2 : 3;
}
function main(): i32 {
  console.log(grade(95));
  console.log(grade(70));
  console.log(grade(30));
  return 0;
}
