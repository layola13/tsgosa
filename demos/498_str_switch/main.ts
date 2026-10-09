function pick(s: string): i32 {
  switch (s) {
    case "a":
      return 1;
    case "b":
      return 2;
    default:
      return 0;
  }
}
function main(): i32 {
  console.log(pick("a"));
  console.log(pick("z"));
  return 0;
}
