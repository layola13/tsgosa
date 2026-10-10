interface U { name: string; age: i32; }
function main(): i32 {
  const u: U = { name: "abcd", age: 5 };
  console.log(u.name.length);
  console.log(u.name[1]);
  return 0;
}
