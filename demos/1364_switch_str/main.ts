function grade(s: string): i32 {
  switch (s) {
    case "a": return 1;
    case "b": return 2;
    default: return 3;
  }
}
function main(): i32 {
  console.log(grade("a"));
  console.log(grade("b"));
  console.log(grade("z"));
  return 0;
}
