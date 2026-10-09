function nonempty(s: string): i32 {
  if (s) {
    return 1;
  }
  return 0;
}
function nullish(c: string | null): i32 {
  if (c) {
    return 1;
  }
  return 0;
}
function main(): i32 {
  console.log("b" > "a" ? 1 : 0);
  console.log("a" >= "a" ? 1 : 0);
  console.log("abc" === "abc" ? 1 : 0);
  console.log(nonempty("x"));
  console.log(nonempty(""));
  if ("") {
    console.log(9);
  } else {
    console.log(0);
  }
  console.log(nullish("a"));
  console.log(nullish(""));
  console.log(nullish(null));
  return 0;
}
