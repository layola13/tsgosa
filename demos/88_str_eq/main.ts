function pick(s: string): i32 {
  if (s == "hi") {
    return 1;
  }
  return 0;
}
function main(): i32 {
  console.log(pick("hi"), pick("yo"));
  return 0;
}
