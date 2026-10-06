function main(): i32 {
  let s = "a";
  s += "b";
  console.log(s);
  s = s + "c";
  console.log(s);
  for (let i = 0; i < 2; i++) {
    s += "!";
  }
  console.log(s);
  return 0;
}
