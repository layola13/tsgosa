function main(): i32 {
  const s: string = "a1b2";
  console.log(s.search("1"));
  console.log(s.search("9"));
  console.log(s.search("b2"));
  const re = /b/;
  console.log(s.search(re));
  console.log(s.search(/9/));
  return 0;
}
