function main(): i32 {
  console.log("abc123".match(/[0-9]+/)[0] === "123" ? 1 : 0);
  console.log("a,b".split(",")[0] === "a" ? 1 : 0);
  return 0;
}
