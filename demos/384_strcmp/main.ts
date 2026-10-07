function cmp(a: string, b: string): i32 {
  if (a < b) {
    return -1;
  }
  if (a > b) {
    return 1;
  }
  return 0;
}
function main(): i32 {
  console.log(cmp("ab", "abc"));
  console.log(cmp("abc", "ab"));
  console.log(cmp("ab", "ab"));
  console.log("x" <= "x");
  console.log("x" >= "y");
  return 0;
}
