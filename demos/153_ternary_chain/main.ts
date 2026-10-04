function grade(s: i32): i32 {
  const g: i32 = s >= 90 ? 1 : s >= 60 ? 2 : 3;
  return g;
}
function main(): i32 {
  console.log(grade(95), grade(70), grade(30));
  return 0;
}