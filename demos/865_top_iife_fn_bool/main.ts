function main(): i32 {
  console.log((function (x: i32) {
    return x * 3;
  })(4));
  console.log(((f: boolean) => f ? 10 : 20)(true));
  return 0;
}
