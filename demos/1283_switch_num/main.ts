function grade(n: i32): i32 {
  switch (n) {
    case 1: return 10;
    case 2: return 20;
    default: return 30;
  }
}
function main(): i32 {
  console.log(grade(1));
  console.log(grade(2));
  console.log(grade(9));
  return 0;
}
