function main(): i32 {
  const s: string = "abcd";
  let t: i32 = 0;
  for (let i: i32 = 0; i < s.length; i++) {
    t = t + s.charCodeAt(i);
  }
  console.log(t);
  return 0;
}